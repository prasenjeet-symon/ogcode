"""Tests for the reusable, content-keyed benchmark index.

A fake ``ogcode`` stands in for the real binary: it answers ``version`` and,
on ``index``, writes a project database in the shape ogcode leaves behind —
document pages whose paths are absolute, plus the session and credential rows
that must never reach the cache.
"""

from __future__ import annotations

import dataclasses
import os
import pathlib
import sqlite3
import sys
import tempfile
import unittest

try:
    from . import support
except ImportError:
    import support

from benchlib import indexcache, providers

FAKE_OGCODE = """#!/usr/bin/env python3
import pathlib
import sqlite3
import sys

if len(sys.argv) > 1 and sys.argv[1] == "version":
    print("ogcode fake-1.0.0")
    raise SystemExit(0)

if not (len(sys.argv) > 1 and sys.argv[1] == "index"):
    raise SystemExit(0)

root = pathlib.Path.cwd()
(root / ".ogcode").mkdir(exist_ok=True)
con = sqlite3.connect(root / ".ogcode" / "ogcode.db")
con.executescript(\"\"\"
create table if not exists doc_page_index(id text primary key, doc_path text, page_num integer, keywords text, labels text, indexed_at integer, mod_time integer);
create table if not exists index_excludes(id text primary key, directory text, pattern text, created_at integer);
create table if not exists index_exclude_seed(directory text primary key, seeded_at integer);
create table if not exists session(id text primary key, cwd text);
create table if not exists message(id text primary key);
create table if not exists part(id text primary key);
create table if not exists usage_event(id text primary key);
create table if not exists note(id text primary key);
create table if not exists provider_config(id text primary key, api_key text);
create table if not exists ogx_account(id text primary key, token text);
create table if not exists index_consent(directory text primary key, allowed integer, time_updated integer);
\"\"\")
docs = [
    path
    for path in root.rglob("*")
    if path.is_file() and not ({".ogcode", ".git"} & set(path.parts))
]
for number, doc in enumerate(sorted(docs)):
    con.execute(
        "insert or replace into doc_page_index values (?, ?, ?, ?, ?, ?, ?)",
        (f"page-{number:04d}", str(doc), 1, "key", "label", 1, 1000),
    )
con.execute("insert or replace into index_excludes values ('exclude-1', ?, '*', 1)", (str(root),))
con.execute("insert or replace into index_exclude_seed values (?, 1)", (str(root),))
con.execute("insert or replace into session values ('session-1', ?)", (str(root),))
con.execute("insert or replace into message values ('message-1')")
con.execute("insert or replace into part values ('part-1')")
con.execute("insert or replace into usage_event values ('usage-1')")
con.execute("insert or replace into note values ('note-1')")
con.execute("insert or replace into provider_config values ('provider-1', 'sk-secret')")
con.execute("insert or replace into ogx_account values ('account-1', 'token-secret')")
con.execute("insert or replace into index_consent values (?, 1, 1)", (str(root),))
con.commit()
con.close()
print("index built")
"""

RUN_STATE = (
    "session",
    "message",
    "part",
    "usage_event",
    "note",
    "provider_config",
    "ogx_account",
    "index_consent",
)


class FakeContext:
    """The parts of ``runner.RunContext`` a cache operation reads."""

    def __init__(self, config, provider, binary, cache_dir, version="ogcode fake-1.0.0"):
        self.config = config
        self.provider = provider
        self.binaries = {"ogcode": str(binary)}
        self.provider_env = dict(os.environ)
        self.index_cache = cache_dir
        self.index_rebuild = False
        self.ogcode_version = version


class IndexCacheTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name)
        self.cache = self.tmp / "cache"
        self.binary = self.tmp / "ogcode"
        self.binary.write_text(FAKE_OGCODE)
        self.binary.chmod(0o755)
        self.cfg = support.load_config_data(
            self.tmp, self.config_data(tasks=["py-ledger"]), "config.json"
        )
        self.provider = providers.resolve_provider(self.cfg.provider)
        self.context = FakeContext(
            self.cfg, self.provider, self.binary, self.cache
        )

    @staticmethod
    def config_data(tasks: list[str], pricing=None) -> dict:
        data = {
            "name": "fake-index",
            "provider": {
                "kind": "custom",
                "base_url": "http://127.0.0.1:9/v1",
                "api_key_env": "BENCHMARK_API_KEY",
            },
            "model": {"id": "fake-model"},
            "run": {
                "reps": 1,
                "tasks": tasks,
                "agents": ["ogcode"],
                "index_timeout_s": 60,
                "output_dir": "results",
            },
        }
        if pricing is not None:
            data["model"]["pricing"] = pricing
        return data

    def spec(self, **overrides):
        spec = indexcache.Spec.of(
            "py-ledger",
            config=self.cfg,
            provider=self.provider,
            ogcode_version=self.context.ogcode_version,
            cache_dir=self.cache,
        )
        return dataclasses.replace(spec, **overrides) if overrides else spec

    def paths(self, db: pathlib.Path):
        con = sqlite3.connect(db)
        try:
            return [row[0] for row in con.execute("select doc_path from doc_page_index")]
        finally:
            con.close()

    def test_entry_is_keyed_by_fixture_model_provider_and_version(self) -> None:
        base = self.spec()
        self.assertEqual(base.key, self.spec().key)
        self.assertEqual(base.entry.name, f"py-ledger-{base.key[:12]}")
        for overrides in (
            {"fixture_sha256": "0" * 64},
            {"model": "other-model"},
            {"provider": "anthropic"},
            {"base_url": "http://other.example/v1"},
            {"ogcode_version": "ogcode 9.9.9"},
        ):
            with self.subTest(overrides=overrides):
                self.assertNotEqual(base.key, dataclasses.replace(base, **overrides).key)

    def test_build_stores_a_scrubbed_entry(self) -> None:
        spec = self.spec()
        result = indexcache.build(spec, self.context)
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["origin"], "built")
        self.assertGreater(result["pages"], 0)
        self.assertTrue(spec.db.is_file())

        meta = indexcache.read_meta(spec)
        self.assertEqual(meta["key"], spec.key)
        self.assertEqual(meta["pages"], result["pages"])
        self.assertFalse(pathlib.Path(meta["source_root"]).exists())

        con = sqlite3.connect(spec.db)
        try:
            for table in RUN_STATE:
                with self.subTest(table=table):
                    self.assertEqual(
                        con.execute(f"select count(*) from {table}").fetchone()[0], 0
                    )
            self.assertEqual(con.execute("select count(*) from index_excludes").fetchone()[0], 1)
        finally:
            con.close()
        self.assertEqual(len(self.paths(spec.db)), result["pages"])

    def test_restore_rewrites_paths_and_drops_run_state(self) -> None:
        spec = self.spec()
        self.assertTrue(indexcache.build(spec, self.context)["ok"])
        source_root = indexcache.read_meta(spec)["source_root"]

        work = self.tmp / "work"
        (work / "src").mkdir(parents=True)
        (work / "src" / "ledger.py").write_text("print('hi')\n")
        restored = indexcache.restore(spec, work)
        self.assertIsNotNone(restored)
        self.assertGreater(restored["pages"], 0)

        db = work / ".ogcode" / "ogcode.db"
        paths = self.paths(db)
        self.assertTrue(paths)
        for path in paths:
            self.assertTrue(path.startswith(str(work.resolve()) + "/"), path)
            self.assertNotIn(source_root, path)

        con = sqlite3.connect(db)
        try:
            self.assertEqual(
                con.execute("select directory from index_excludes").fetchone()[0],
                str(work.resolve()),
            )
            self.assertEqual(
                con.execute("select directory from index_exclude_seed").fetchone()[0],
                str(work.resolve()),
            )
            self.assertEqual(
                con.execute("select min(mod_time) from doc_page_index").fetchone()[0] > 1000,
                True,
            )
            for table in RUN_STATE:
                with self.subTest(table=table):
                    self.assertEqual(
                        con.execute(f"select count(*) from {table}").fetchone()[0], 0
                    )
        finally:
            con.close()

    def test_rewrite_root_escapes_like_wildcards(self) -> None:
        db = self.tmp / "wild.db"
        con = sqlite3.connect(db)
        try:
            con.executescript(
                "create table doc_page_index(id text primary key, doc_path text);"
                "create table index_excludes(id text primary key, directory text);"
                "create table index_exclude_seed(directory text primary key);"
            )
            old = "/tmp/a_b%/repo"
            con.execute("insert into doc_page_index values ('1', ?)", (old + "/src/main.py",))
            con.execute(
                "insert into doc_page_index values ('2', ?)",
                ("/tmp/a_b%/repository/other.py",),
            )
            con.execute("insert into index_excludes values ('1', ?)", (old,))
            con.execute("insert into index_exclude_seed values (?)", (old,))
            con.commit()

            indexcache.rewrite_root(con, old, "/new/r%oot/repo")
            con.commit()

            rows = dict(con.execute("select id, doc_path from doc_page_index"))
            self.assertEqual(rows["1"], "/new/r%oot/repo/src/main.py")
            self.assertEqual(rows["2"], "/tmp/a_b%/repository/other.py")
            self.assertEqual(
                con.execute("select directory from index_excludes").fetchone()[0],
                "/new/r%oot/repo",
            )
            self.assertEqual(
                con.execute("select directory from index_exclude_seed").fetchone()[0],
                "/new/r%oot/repo",
            )
        finally:
            con.close()

    def test_acquire_builds_once_then_restores(self) -> None:
        first_work = self.tmp / "first"
        first_work.mkdir()
        first = indexcache.acquire(
            "py-ledger", first_work, self.context, self.tmp / "logs-first"
        )
        self.assertTrue(first["ok"], first)
        self.assertEqual(first["origin"], "built")

        second_work = self.tmp / "second"
        second_work.mkdir()
        second = indexcache.acquire(
            "py-ledger", second_work, self.context, self.tmp / "logs-second"
        )
        self.assertTrue(second["ok"], second)
        self.assertEqual(second["origin"], "restored")
        self.assertEqual(second["pages"], first["pages"])
        self.assertTrue(
            (self.tmp / "logs-second" / "index.stdout.log")
            .read_text()
            .startswith("index: restored")
        )
        for path in self.paths(second_work / ".ogcode" / "ogcode.db"):
            self.assertTrue(path.startswith(str(second_work.resolve()) + "/"), path)

    def test_prebuild_skips_cached_entries_unless_forced(self) -> None:
        spec = self.spec()
        self.assertTrue(indexcache.build(spec, self.context)["ok"])

        cached = indexcache.prebuild([spec], self.context)
        self.assertEqual(cached[0]["origin"], "cached")
        self.assertEqual(cached[0]["duration_s"], 0.0)
        self.assertEqual(cached[0]["task"], "py-ledger")

        forced = indexcache.prebuild([spec], self.context, force=True)
        self.assertEqual(forced[0]["origin"], "built")
        self.assertTrue(forced[0]["ok"])

    def test_build_index_flag_fills_the_cache_and_exits(self) -> None:
        cfg_path = support.write_config(
            self.tmp, self.config_data(tasks=["py-ledger"]), "fake.json"
        )
        env = support.cli_env(BENCHMARK_API_KEY="test-key")
        args = (
            "--config",
            str(cfg_path),
            "--build-index",
            "--index-cache",
            str(self.cache),
            "--ogcode",
            str(self.binary),
            "--opencode",
            sys.executable,
        )

        proc = support.run_cli(*args, env=env)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertIn("built", proc.stdout)
        self.assertIn("1 index(es) ready", proc.stdout)
        entries = list(self.cache.glob("py-ledger-*"))
        self.assertEqual(len(entries), 1, entries)
        self.assertTrue((entries[0] / "index.json").is_file())
        self.assertTrue((entries[0] / "ogcode.db").is_file())

        again = support.run_cli(*args, env=env)
        self.assertEqual(again.returncode, 0, again.stdout + again.stderr)
        self.assertIn("cached", again.stdout)

        rebuilt = support.run_cli(*args, "--rebuild-index", env=env)
        self.assertEqual(rebuilt.returncode, 0, rebuilt.stdout + rebuilt.stderr)
        self.assertIn("built", rebuilt.stdout)


if __name__ == "__main__":
    unittest.main()
