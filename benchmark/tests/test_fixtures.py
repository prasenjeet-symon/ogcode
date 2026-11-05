"""Fixture preparation, drift detection and red/green self-check tests."""

from __future__ import annotations

import hashlib
import json
import pathlib
import shutil
import tempfile
import unittest
from unittest import mock

try:
    from . import support
except ImportError:
    import support

from benchlib import fixtures

TOOLCHAINS = {
    "go-bugfix": ("go",),
    "js-bugfix": ("node",),
    "py-bugfix": ("python3",),
    "py-feature": ("python3",),
    "py-ledger": ("python3",),
    "py-shop": ("python3",),
}


class FixtureTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name)

    def make_tree(self, root: pathlib.Path, files: dict[str, str]) -> pathlib.Path:
        for rel, content in files.items():
            path = root / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        return root


class TreeTest(FixtureTestCase):
    def test_tree_bytes_skips_junk_directories(self) -> None:
        root = self.tmp / "repo"
        self.make_tree(
            root,
            {
                "src/main.py": "print('ok')\n",
                "keep.txt": "keep\n",
                ".ogcode/state.json": "{}",
                "ogcode.json": "{}",
                "__pycache__/main.cpython-314.pyc": "binary-ish",
                "src/__pycache__/other.pyc": "binary-ish",
                "node_modules/pkg/index.js": "module",
                "node_modules/pkg/node_modules/nested/index.js": "module",
                ".pytest_cache/v/cache": "cache",
                ".opencode/config.json": "{}",
                "src/leftover.pyc": "binary-ish",
            },
        )
        self.assertEqual(
            sorted(fixtures.tree_bytes(root)),
            ["keep.txt", "ogcode.json", "src/leftover.pyc", "src/main.py"],
        )

    def test_tree_bytes_is_content_addressed(self) -> None:
        root = self.tmp / "tree"
        self.make_tree(root, {"a.txt": "alpha", "b/c.txt": "gamma"})
        first = fixtures.tree_bytes(root)
        second = fixtures.tree_bytes(root)
        self.assertEqual(first, second)
        self.assertEqual(first["a.txt"], b"alpha")
        self.assertEqual(first["b/c.txt"], b"gamma")

    def test_diff_stats_counts_modified_and_added_files(self) -> None:
        work = self.tmp / "work"
        rev = fixtures.prepare("py-bugfix", work)
        self.assertRegex(rev, r"^[0-9a-f]{40}$")

        clean = fixtures.diff_stats(work)
        self.assertEqual(
            clean, {"files": 0, "added": 0, "removed": 0, "paths": []}
        )

        target = sorted(path for path in work.rglob("*.py") if path.is_file())[0]
        target.write_text(target.read_text() + "\n# test change\n")
        (work / "added.txt").write_text("one\ntwo\n")

        stats = fixtures.diff_stats(work)
        self.assertEqual(stats["files"], 2)
        self.assertGreaterEqual(stats["added"], 3)
        self.assertIn("added.txt", stats["paths"])
        self.assertIn(str(target.relative_to(work)), stats["paths"])

    def test_diff_stats_outside_a_git_repository_is_empty(self) -> None:
        plain = self.tmp / "plain"
        plain.mkdir()
        (plain / "file.txt").write_text("not a repo")
        self.assertEqual(
            fixtures.diff_stats(plain),
            {"files": 0, "added": 0, "removed": 0, "paths": []},
        )


class PrepareTest(FixtureTestCase):
    def test_prepare_commits_the_fixture_and_excludes_isolation_config(self) -> None:
        work = self.tmp / "work"
        rev = fixtures.prepare("py-bugfix", work)
        self.assertRegex(rev, r"^[0-9a-f]{40}$")

        self.assertTrue((work / "ogcode.json").is_file())
        isolation = json.loads((work / "ogcode.json").read_text())
        self.assertEqual(isolation["skills"]["permissions"]["*"], "deny")

        exclude = (work / ".git" / "info" / "exclude").read_text()
        self.assertIn("node_modules/", exclude)

        tracked = support.util.git(work, "ls-files")
        self.assertNotIn("ogcode.json", tracked.splitlines())
        self.assertEqual(support.util.git(work, "status", "--porcelain"), "")
        self.assertEqual(fixtures.diff_stats(work)["files"], 0)

    def test_task_prompt_is_the_readme_text(self) -> None:
        prompt = fixtures.task_prompt("py-bugfix")
        self.assertTrue(prompt)
        self.assertEqual(
            prompt,
            (support.TASKS_DIR / "py-bugfix" / "task.md").read_text().strip(),
        )

    def test_task_prompt_rejects_an_empty_prompt(self) -> None:
        tasks_root = self.tmp / "tasks"
        self.make_tree(tasks_root, {"empty/task.md": " \n"})
        with mock.patch.object(fixtures, "TASKS_DIR", tasks_root):
            with self.assertRaises(SystemExit) as caught:
                fixtures.task_prompt("empty")
        self.assertIn("task empty has an empty task.md", str(caught.exception))

    def test_discover_tasks_requires_task_markdown(self) -> None:
        tasks_root = self.tmp / "tasks"
        self.make_tree(
            tasks_root,
            {
                "beta/task.md": "beta",
                "alpha/task.md": "alpha",
                "no-prompt/README.md": "not a task",
            },
        )
        (tasks_root / "empty-dir").mkdir()
        with mock.patch.object(fixtures, "TASKS_DIR", tasks_root):
            self.assertEqual(fixtures.discover_tasks(), ["alpha", "beta"])


class DigestTest(unittest.TestCase):
    def test_digest_is_stable_and_task_specific(self) -> None:
        first = fixtures.fixture_digest("py-bugfix")
        self.assertEqual(first, fixtures.fixture_digest("py-bugfix"))
        self.assertRegex(first, r"^[0-9a-f]{64}$")

        digests = {task: fixtures.fixture_digest(task) for task in support.SIX_TASKS}
        self.assertEqual(len(set(digests.values())), len(support.SIX_TASKS))

    def test_digest_matches_a_manual_sha256(self) -> None:
        task = "py-ledger"
        expected = hashlib.sha256()
        for rel, content in sorted(
            fixtures.tree_bytes(support.TASKS_DIR / task / "repo").items()
        ):
            expected.update(rel.encode())
            expected.update(b"\0")
            expected.update(content)
        self.assertEqual(fixtures.fixture_digest(task), expected.hexdigest())


class SnapshotTest(FixtureTestCase):
    def setUp(self) -> None:
        super().setUp()
        self.tasks_root = self.tmp / "tasks"
        self.make_tree(
            self.tasks_root,
            {
                "alpha/repo/tracked.txt": "source-v1\n",
                "alpha/repo/sub/data.json": '{"v": 1}\n',
                "beta/repo/only.txt": "beta\n",
            },
        )
        self.pristine = self.tmp / "pristine"

    def test_snapshot_refreshes_an_existing_copy(self) -> None:
        with mock.patch.object(fixtures, "TASKS_DIR", self.tasks_root):
            fixtures.snapshot_fixtures(["alpha"], self.pristine)
            stale = self.pristine / "alpha" / "tracked.txt"
            self.assertEqual(stale.read_text(), "source-v1\n")

            # A stale baseline must not survive a new run.
            stale.write_text("stale-baseline\n")
            (self.tasks_root / "alpha" / "repo" / "tracked.txt").write_text(
                "source-v2\n"
            )
            fixtures.snapshot_fixtures(["alpha"], self.pristine)

        self.assertEqual(stale.read_text(), "source-v2\n")

    def test_drift_paths_reports_added_removed_and_changed(self) -> None:
        pristine = self.make_tree(
            self.tmp / "pristine2",
            {"same.txt": "same", "gone.txt": "gone", "changed.txt": "before"},
        )
        live = self.make_tree(
            self.tmp / "live",
            {"same.txt": "same", "changed.txt": "after", "new.txt": "new"},
        )
        self.assertEqual(
            fixtures.drift_paths(pristine, live),
            ["~changed.txt", "-gone.txt", "+new.txt"],
        )
        self.assertEqual(fixtures.drift_paths(pristine, pristine), [])

    def test_restore_repairs_a_drifted_live_fixture(self) -> None:
        with mock.patch.object(fixtures, "TASKS_DIR", self.tasks_root):
            fixtures.snapshot_fixtures(["alpha"], self.pristine)
            live = self.tasks_root / "alpha" / "repo"
            (live / "tracked.txt").write_text("agent changed this\n")
            (live / "extra.txt").write_text("agent added this\n")

            repaired = fixtures.restore_fixtures(["alpha"], self.pristine)

            self.assertEqual(repaired, ["alpha: +extra.txt, ~tracked.txt"])
            self.assertEqual((live / "tracked.txt").read_text(), "source-v1\n")
            self.assertFalse((live / "extra.txt").exists())

    def test_restore_without_a_snapshot_is_a_no_op(self) -> None:
        live = self.tasks_root / "beta" / "repo"
        before = (live / "only.txt").read_text()
        with mock.patch.object(fixtures, "TASKS_DIR", self.tasks_root):
            repaired = fixtures.restore_fixtures(["beta"], self.tmp / "no-snapshot")
        self.assertEqual(repaired, [])
        self.assertEqual((live / "only.txt").read_text(), before)


class RedGreenTest(FixtureTestCase):
    """The slow test: every checked-in fixture must fail before the fix and
    pass after the reference solution is applied.  The runs copy each fixture
    into a temporary directory, so nothing checked in is modified."""

    @unittest.skipUnless(shutil.which("git"), "git is required for fixtures")
    def test_all_six_fixtures_are_red_then_green(self) -> None:
        env = support.providers.scrub_provider_env()
        env["NO_COLOR"] = "1"

        for task in support.SIX_TASKS:
            with self.subTest(task=task):
                missing = next(
                    (binary for binary in TOOLCHAINS[task] if not shutil.which(binary)),
                    None,
                )
                if missing:
                    self.skipTest(f"{missing} is not installed")

                result = fixtures.check_task(task, 120, env)
                self.assertTrue(
                    result["red_as_expected"],
                    msg=f"{task} passed before the fix:\n{result['red_output']}",
                )
                self.assertTrue(
                    result["reference_green"],
                    msg=f"{task} failed after the reference fix:\n{result['green_output']}",
                )


if __name__ == "__main__":
    unittest.main()
