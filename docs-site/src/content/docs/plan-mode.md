---
title: Plan mode & tasks
description: Break a feature into tasks, each in its own worktree, and open pull requests.
---

A single Build turn is right for a change you can describe in a paragraph. For something larger — a feature that touches many files, or a set of related fixes — **Plan mode** turns the goal into a board of tasks it can work through, each isolated from the others.

## The flow

1. Start Plan mode:

   ```bash
   ogcode plan
   ```

2. Describe the outcome you want. The planning agent inspects the repository and works out an approach with you.
3. **Review the plan** before anything runs. It becomes a board of tasks with descriptions and dependencies, and you approve it — nothing is executed until you do.
4. The tasks run, independent ones in parallel, each in its **own git worktree** off the base branch, so changes never collide with each other or with your working tree.
5. When a task finishes it can commit, push, and **open a pull request**, and a failed task can be retried from a clean state.

## Why worktrees

A worktree is a separate checkout of the same repository on its own branch. Because every task gets one, parallel work cannot step on itself — two tasks editing the same project do not fight over the same files, and your own checkout stays exactly as you left it. The result is a set of reviewable branches and pull requests rather than one tangled set of edits.

Dependent tasks share a branch and run in order, which keeps a chain of changes coherent while still letting unrelated work run at the same time.

## Headless

Breakdown — splitting a goal into tasks without executing anything — is available without the browser:

```bash
ogcode run -a breakdown "split the auth refactor into tasks"
```

This is useful for previewing how a large goal would be carved up before you commit to running it.

## When to use it

- Reach for **Build** mode for a change you can hold in your head.
- Reach for **Plan mode** when the work is large enough that you want to review the shape of it first, or when several pieces can usefully run at once.

## Next

- [Permissions](/docs/permissions/) — how approvals work when many tasks run at once.
- [Remote deployment](/docs/deployment/) — running plans on a server rather than your laptop.
