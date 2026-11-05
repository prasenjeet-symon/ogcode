"""A reusable ogcode document index for benchmark fixtures.

Building the index is setup, not measurement: it is paid for outside the
agent's window and excluded from every reported figure.  It is also the same
work on every repetition of a task and on every campaign that uses the same
fixture, model, provider and binary, so the benchmark builds it once and
restores the result into each attempt's worktree.

Restoring is not a file copy.  ``doc_page_index.doc_path`` holds the absolute
path of the tree the index was built in, so the entry is keyed by fixture
content and its paths are rewritten to the attempt's worktree.  The database
is ogcode's whole project database — sessions, messages, usage and provider
credentials — so everything except the index itself is cleared before an entry
is stored.

A cached entry is only reused when the fixture digest, model, provider, base
URL and binary version all match, because the labels are model-generated: an
index built by one model is not a valid map for another.
"""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import shutil
import sqlite3
import tempfile
import time
from dataclasses import dataclass
from typing import Any, Optional

from . import fixtures, util

SCHEMA = 1
DB_NAME = "ogcode.db"
META_NAME = "index.json"

# Rows that belong to the run that built the index, not to the index.  The
# cache is copied out of a project database that also held a session, its
# messages and the provider credentials used to reach the model.
RUN_STATE_TABLES = (
    "part",
    "message",
    "session",
    "usage_event",
    "note",
    "provider_config",
    "ogx_account",
    "index_consent",
)


def default_cache_dir() -> pathlib.Path:
    """Where entries live when the caller does not name a directory."""
    root = os.environ.get("XDG_CACHE_HOME")
    base = pathlib.Path(root).expanduser() if root else pathlib.Path.home() / ".cache"
    return (base / "ogcode-benchmark" / "indexes").resolve()


@dataclass(frozen=True)
class Spec:
    """Everything that decides whether one cached index may be reused."""

    task: str
    fixture_sha256: str
    model: str
    provider: str
    base_url: Optional[str]
    ogcode_version: str
    cache_dir: pathlib.Path

    @classmethod
    def of(
        cls,
        task: str,
        *,
        config: Any,
        provider: Any,
        ogcode_version: str,
        cache_dir: pathlib.Path,
    ) -> "Spec":
        return cls(
            task=task,
            fixture_sha256=fixtures.fixture_digest(task),
            model=config.agent_model("ogcode"),
            provider=provider.kind,
            base_url=provider.base_url,
            ogcode_version=ogcode_version,
            cache_dir=pathlib.Path(cache_dir).expanduser().resolve(),
        )

    def identity(self) -> dict[str, Any]:
        """The key material, also written into the entry for inspection."""
        return {
            "schema": SCHEMA,
            "task": self.task,
            "fixture_sha256": self.fixture_sha256,
            "model": self.model,
            "provider": self.provider,
            "base_url": self.base_url,
            "ogcode_version": self.ogcode_version,
        }

    @property
    def key(self) -> str:
        blob = json.dumps(self.identity(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(blob.encode()).hexdigest()

    @property
    def entry(self) -> pathlib.Path:
        return self.cache_dir / f"{self.task}-{self.key[:12]}"

    @property
    def db(self) -> pathlib.Path:
        return self.entry / DB_NAME

    @property
    def meta(self) -> pathlib.Path:
        return self.entry / META_NAME

    @property
    def logs(self) -> pathlib.Path:
        return self.entry / "logs"


def index_pages(db: pathlib.Path) -> int:
    """Number of pages in ogcode's document index, or zero if absent."""
    if not db.exists():
        return 0
    try:
        conn = sqlite3.connect(db)
        try:
            row = conn.execute("select count(*) from doc_page_index").fetchone()
        finally:
            conn.close()
    except sqlite3.Error:
        return 0
    return int(row[0]) if row else 0


def read_meta(spec: Spec) -> Optional[dict[str, Any]]:
    """The entry's identity and provenance, or None when it is unreadable."""
    try:
        return json.loads(spec.meta.read_text())
    except (OSError, ValueError):
        return None


def is_cached(spec: Spec) -> bool:
    """True when this entry exists, matches the key and still has pages."""
    meta = read_meta(spec)
    if not meta or meta.get("key") != spec.key or int(meta.get("pages") or 0) <= 0:
        return False
    return spec.db.is_file() and index_pages(spec.db) > 0


def _escape_like(value: str) -> str:
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


def _table_exists(con: sqlite3.Connection, name: str) -> bool:
    row = con.execute(
        "select 1 from sqlite_master where type = 'table' and name = ?", (name,)
    ).fetchone()
    return row is not None


def _rewrite_tree(
    con: sqlite3.Connection,
    table: str,
    column: str,
    old_root: str,
    new_root: str,
) -> None:
    """Move one path column from the build root to the new worktree."""
    if not _table_exists(con, table):
        return
    con.execute(
        f"update {table} set {column} = ? where {column} = ?",
        (new_root, old_root),
    )
    prefix = old_root + "/"
    con.execute(
        f"update {table} set {column} = ? || substr({column}, ?) "
        f"where {column} like ? escape '\\'",
        (new_root + "/", len(prefix) + 1, _escape_like(prefix) + "%"),
    )


def rewrite_root(con: sqlite3.Connection, old_root: str, new_root: str) -> None:
    """Point every stored path at the worktree the index is restored into."""
    old_root = str(old_root or "").rstrip("/")
    new_root = str(new_root or "").rstrip("/")
    if not old_root or not new_root or old_root == new_root:
        return
    for table, column in (
        ("doc_page_index", "doc_path"),
        ("index_excludes", "directory"),
        ("index_exclude_seed", "directory"),
    ):
        _rewrite_tree(con, table, column, old_root, new_root)


def _reset_run_state(con: sqlite3.Connection) -> None:
    """Drop everything that is not the index itself."""
    for table in RUN_STATE_TABLES:
        if _table_exists(con, table):
            con.execute(f"delete from {table}")
    con.commit()


def _copy_db(source: pathlib.Path, dest: pathlib.Path) -> None:
    """Copy a SQLite database without leaving a WAL behind."""
    dest.parent.mkdir(parents=True, exist_ok=True)
    for suffix in ("-wal", "-shm"):
        stale = pathlib.Path(str(dest) + suffix)
        if stale.exists():
            stale.unlink()
    if dest.exists():
        dest.unlink()
    src = sqlite3.connect(str(source))
    try:
        dst = sqlite3.connect(str(dest))
        try:
            src.backup(dst)
            dst.execute("pragma journal_mode=delete")
            dst.commit()
        finally:
            dst.close()
    finally:
        src.close()


def _write_logs(directory: Optional[pathlib.Path], stdout: str, stderr: str) -> None:
    if directory is None:
        return
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "index.stdout.log").write_text(stdout)
    (directory / "index.stderr.log").write_text(stderr)


def _copy_logs(source: pathlib.Path, dest: Optional[pathlib.Path]) -> None:
    if dest is None or dest.resolve() == source.resolve():
        return
    dest.mkdir(parents=True, exist_ok=True)
    for name in ("index.stdout.log", "index.stderr.log"):
        incoming = source / name
        if incoming.is_file():
            shutil.copy2(incoming, dest / name)


def _store(
    spec: Spec,
    source_db: pathlib.Path,
    source_root: pathlib.Path,
    pages: int,
    build_s: float,
) -> None:
    """Keep a scrubbed copy of the index in the cache entry."""
    spec.entry.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{DB_NAME}.", dir=str(spec.entry))
    os.close(fd)
    tmp = pathlib.Path(tmp_name)
    try:
        _copy_db(source_db, tmp)
        con = sqlite3.connect(str(tmp))
        try:
            _reset_run_state(con)
            con.execute("pragma journal_mode=delete")
            con.commit()
        finally:
            con.close()
        os.replace(tmp, spec.db)
    finally:
        if tmp.exists():
            tmp.unlink()
    meta = spec.identity()
    meta.update(
        {
            "key": spec.key,
            "pages": pages,
            "source_root": str(pathlib.Path(source_root).resolve()),
            "built_at": util.iso_now(),
            "build_s": round(build_s, 3),
        }
    )
    util.write_json(spec.meta, meta)


def build(
    spec: Spec,
    context: Any,
    log_dir: Optional[pathlib.Path] = None,
) -> dict[str, Any]:
    """Build one entry from a scratch copy of the fixture."""
    work = pathlib.Path(
        tempfile.mkdtemp(prefix=f"ogcode-benchmark-index-{spec.task}-")
    )
    try:
        fixtures.prepare(spec.task, work)
        cmd = [
            context.binaries["ogcode"],
            "index",
            "--model",
            context.config.agent_model("ogcode"),
        ]
        env = dict(context.provider_env)
        spec.logs.mkdir(parents=True, exist_ok=True)
        env["OGCODE_LOG_DIR"] = str(spec.logs)
        proc = util.run_cmd(
            cmd, work, context.config.run.index_timeout_s, env, sample_resources=True
        )
        _write_logs(spec.logs, proc["stdout"], proc["stderr"])
        _copy_logs(spec.logs, log_dir)

        pages = index_pages(work / ".ogcode" / DB_NAME)
        output = (proc["stderr"] or proc["stdout"]).strip()
        if proc["code"] != 0 or pages <= 0:
            return {
                "ok": False,
                "origin": "built",
                "pages": pages,
                "duration_s": proc["duration_s"],
                "resources": proc.get("resources"),
                "error": output[-800:] or "index produced no pages",
                "key": spec.key,
            }
        _store(spec, work / ".ogcode" / DB_NAME, work, pages, proc["duration_s"])
        return {
            "ok": True,
            "origin": "built",
            "pages": pages,
            "duration_s": proc["duration_s"],
            "resources": proc.get("resources"),
            "error": None,
            "key": spec.key,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)


def prebuild(
    specs: list[Spec],
    context: Any,
    force: bool = False,
) -> list[dict[str, Any]]:
    """Build every missing entry once, before any attempt starts."""
    results: list[dict[str, Any]] = []
    for spec in specs:
        if not force and is_cached(spec):
            meta = read_meta(spec) or {}
            results.append(
                {
                    "ok": True,
                    "origin": "cached",
                    "pages": int(meta.get("pages") or 0),
                    "duration_s": 0.0,
                    "error": None,
                    "key": spec.key,
                    "task": spec.task,
                }
            )
            continue
        result = build(spec, context)
        result["task"] = spec.task
        results.append(result)
    return results


def restore(spec: Spec, work: pathlib.Path) -> Optional[dict[str, Any]]:
    """Copy a cached entry into ``work`` and repoint its paths."""
    meta = read_meta(spec)
    if not meta or not spec.db.is_file():
        return None
    target = pathlib.Path(work) / ".ogcode" / DB_NAME
    try:
        _copy_db(spec.db, target)
        con = sqlite3.connect(str(target))
        try:
            rewrite_root(
                con,
                str(meta.get("source_root") or ""),
                str(pathlib.Path(work).resolve()),
            )
            _reset_run_state(con)
            # The attempt must not look stale to the refresh guard: the
            # worktree is new, but the index describes it already.
            con.execute(
                "update doc_page_index set mod_time = ?", (int(time.time() * 1000),)
            )
            con.commit()
            row = con.execute("select count(*) from doc_page_index").fetchone()
            pages = int(row[0]) if row else 0
        finally:
            con.close()
    except sqlite3.Error:
        target.unlink(missing_ok=True)
        return None
    return {"pages": pages, "entry": spec.entry}


def acquire(
    task: str,
    work: pathlib.Path,
    context: Any,
    log_dir: Optional[pathlib.Path],
) -> dict[str, Any]:
    """Restore the task's index into ``work``, building it only on a miss."""
    spec = Spec.of(
        task,
        config=context.config,
        provider=context.provider,
        ogcode_version=context.ogcode_version,
        cache_dir=context.index_cache,
    )
    started = time.monotonic()
    restored = restore(spec, work)
    if restored is not None and restored["pages"] > 0:
        _write_logs(
            log_dir,
            f"index: restored {restored['pages']} pages from {spec.entry}\n",
            "",
        )
        return {
            "ok": True,
            "origin": "restored",
            "pages": restored["pages"],
            "duration_s": time.monotonic() - started,
            "error": None,
            "key": spec.key,
        }

    built = build(spec, context, log_dir)
    if not built["ok"]:
        return built
    restored = restore(spec, work)
    if restored is None or restored["pages"] <= 0:
        return {
            "ok": False,
            "origin": "built",
            "pages": 0,
            "duration_s": time.monotonic() - started,
            "error": "index was built but could not be restored into the worktree",
            "key": spec.key,
        }
    return {
        "ok": True,
        "origin": "built",
        "pages": restored["pages"],
        "duration_s": time.monotonic() - started,
        "resources": built.get("resources"),
        "error": None,
        "key": spec.key,
    }
