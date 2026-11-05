# Adding a task

A task is a self-contained fixture under `tasks/`. The harness discovers every
directory that contains a `task.md`; there is no registry to edit and no Python
change to make. A new task changes the composition of the suite, so a
"60-run" sample is only 60 runs while the suite has six tasks.

Read [methodology.md](methodology.md) for the attempt lifecycle and
[providers.md](providers.md) for model and pricing configuration.

## Directory contract

```text
tasks/<name>/
  repo/       starting tree copied into the worktree
  task.md     prompt text passed to both agents (stripped)
  verify.sh   grader run before and after the agent
  reference/  known-good tree overlaid to prove the task is solvable
```

Use a short lowercase-hyphenated name. Task names are selected with
`--tasks name,other` and appear in result paths, so avoid commas and spaces.

### `repo/`

This is the project the agent receives. It must contain the implementation and
the tests, and it must fail `verify.sh` as shipped. The harness copies the tree,
initializes a fresh git repository, commits everything as the fixture revision,
and writes an isolation `ogcode.json` after the commit.

Keep the tree self-contained. The harness does not install dependencies or
fetch anything before a run; a task should use a preinstalled toolchain or
vendor what it needs. Do not ship a `.git` directory, credentials, absolute
paths, build output or caches. The harness excludes `.ogcode/`, `ogcode.json`,
`__pycache__/`, `node_modules/`, `.pytest_cache/`, `.opencode/` and `*.pyc` from
the fixture commit, the measured diff and the pristine comparison, but the
copy into the worktree happens first. Generated files left in `repo/` can
therefore still reach the agent, so remove them.

### `task.md`

The file is stripped of leading and trailing whitespace and passed as one
argument to each agent; it is not interpolated into a shell command. Write the
prompt the way the bundled tasks do:

- name the command that currently fails, and what must pass at the end;
- give the scope the agent needs without giving away the fix;
- state that tests must not be edited, because the grader restores them;
- keep it self-contained and identical for both agents.

An empty `task.md` is an error. Do not put the solution, a patch or
agent-specific instructions in the prompt.

### `verify.sh`

The harness runs:

```sh
sh tasks/<name>/verify.sh <workdir> <base-rev>
```

`<workdir>` is the absolute path of the fresh worktree. The verifier is started
with that directory as its current directory, but it should use `$1` rather
than relying on the caller's directory. `<base-rev>` is the fixture commit, so
the verifier can restore test files that the agent changed. The default
verification timeout is 300 seconds; a measured run can override it with
`run.verify_timeout_s` or `--verify-timeout`.

Exit 0 means solved. Any non-zero exit, including a timeout, means unsolved.
Standard output and standard error are combined and the tail is stored in the
raw row; the harness does not parse the text.

Use POSIX `sh`, quote paths and enable `set -eu`. The script must be
deterministic, must not use the network, and must not write outside the
worktree. A minimal template:

```sh
#!/bin/sh
set -eu

work=$1
rev=$2

# Discard changes the agent made to the grading tests.
git -C "$work" checkout --quiet "$rev" -- test_feature.py

cd "$work"
exec python3 -m unittest -q test_feature
```

Restore every file that can change the result, not only the main test file.
That includes additional test modules, `conftest.py`, fixtures, golden files,
test data and any harness the tests import. If the suite has several test
files or a test directory, restore each of them. The bundled `py-shop`
verifier restores the whole `tests` directory this way.

The verifier is the only pass criterion. There is no human review of the diff
and no hidden test suite, so a verifier that trusts agent-modified tests can be
trivially defeated. Make the command the task asks the agent to run the same
command the verifier runs.

### `reference/`

`reference/` is a known-good tree, not a patch file. After the red check, the
harness copies it over the worktree with `copytree(..., dirs_exist_ok=True)`:
files in `reference/` overwrite or add files at the same relative paths. It is
then verified with the shipped tests.

Include only the implementation files needed to make the task pass. Do not
change tests in `reference/`; the verifier restores them from the fixture
commit before grading. Do not include caches, logs or unrelated files. A
reference tree proves that the task is solvable and the grader can pass; it
does not prove that every valid solution will pass.

## Red/green gate

`check_fixtures.sh` runs the same preparation as a measured run for each
selected task, checks that the shipped fixture fails, overlays `reference/`,
and checks that the result passes. It makes no model calls.

```sh
# All discovered tasks.
sh check_fixtures.sh

# Selected tasks.
sh check_fixtures.sh py-bugfix py-ledger

# A slower fixture.
BENCHMARK_VERIFY_TIMEOUT=600 sh check_fixtures.sh py-shop
```

Successful output looks like:

```text
ok   py-bugfix: red as shipped
ok   py-bugfix: reference fix is green
ALL FIXTURES OK
```

The script exits non-zero if a fixture is already green, a reference tree does
not pass, or a task name is unknown. `BENCHMARK_VERIFY_TIMEOUT` changes the
per-check timeout and defaults to 300 seconds.

## Step by step

1. Create `tasks/<name>/` and add the starting project under `repo/`.
2. Add the failing test or tests and confirm the failure is the one the task is
   about, not a setup error.
3. Write `task.md` around the command the verifier will run.
4. Write `verify.sh`, restoring every test-affecting file from `$2`.
5. Add the minimal implementation changes under `reference/`.
6. Run `sh check_fixtures.sh <name>` until both checks pass.
7. Confirm discovery and configuration without spending money:
   `python3 run_benchmark.py --tasks <name> --dry-run`.
8. Export the selected provider's key, then run one repetition with both agents
   against a real provider from the package root:
   `python3 run_benchmark.py --tasks <name> --reps 1 --out results/<name>-smoke`.
   This spends money.
9. Regenerate the report and inspect the diff, verifier output, turns, tool
   calls and errors:
   `python3 report.py --raw results/<name>-smoke/raw.jsonl`.
10. If the suite changed, update the task table and sample-size wording in
    `README.md`.

## Review checklist

- [ ] `sh check_fixtures.sh <name>` ends with `ALL FIXTURES OK`.
- [ ] The shipped `repo/` fails for the intended reason; the failure is not a
      missing dependency, broken import or absent test file.
- [ ] `task.md` names the failing command, scopes the work, forbids test edits
      and does not reveal the implementation.
- [ ] `verify.sh` restores every test file and test-affecting data file from
      `$2`, uses `$1` and `$2`, quotes paths and exits non-zero on failure.
- [ ] The verifier is deterministic, offline, does not write outside the
      worktree and finishes inside the verification timeout.
- [ ] `reference/` contains implementation changes only, no test edits, no
      caches and no unrelated files.
- [ ] `repo/` and `reference/` contain no `.git`, secrets, absolute paths,
      `__pycache__`, `node_modules`, `.ogcode` or other generated state.
- [ ] A one-repetition smoke run on both agents produces a passing row with a
      diff limited to the intended files.
- [ ] The new fixture's digest is recorded, and the documentation no longer
      describes the old task count or sample size.

Adding or changing a task changes its fixture digest and the suite being
measured. Results from before the change should not be compared with results
from after it without saying so.
