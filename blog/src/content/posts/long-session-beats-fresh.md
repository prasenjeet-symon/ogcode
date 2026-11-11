---
title: "Why a Long Session Beats a Fresh One"
description: "Most agents tell you to start a new session to save tokens, because they re-send the whole conversation on every turn. Ogcode is built the other way: a turn's request never grows with the session's age, the agent keeps notes about the task it is on, and a keyword search finds a year-old conversation in milliseconds. Here is why we tell people to keep one session per task, for as long as the task lives."
pubDate: 2026-11-11
tags: ["agents", "session-memory", "session-search", "context-engine"]
hero: "/blog/media/long-session-beats-fresh-hero.jpg"
heroAlt: "A dark hall of tall glass columns. One column glows orange and is packed with stacked note cards; the other columns stand grey and empty."
ogImage: "https://ogcode.in/blog/media/long-session-beats-fresh-hero.jpg"
---

"Start a new session." It is on every list of tips for coding agents. New session for a new bug. New session when the agent starts repeating itself. New session every morning, in some guides. The reason is always the same: a long conversation is expensive, and the agent gets worse as the window fills.

For most agents that is true, and it is not their fault. It falls out of how they are built.

**In short:** the advice to start over is the right answer to a real problem. An agent that re-sends the whole conversation on every turn pays for its history again and again, so throwing the history away is the cheapest thing you can do. Ogcode does not re-send it. A turn's request is bounded no matter how old the session is, the agent writes down what it has learned about the task, and a keyword search gets you back into the conversation in a few milliseconds. So we tell people the opposite: **one session per task**, kept for as long as the task lives.

## Where the advice comes from

Most agents built on a chat model treat the conversation as the memory. Every turn, the transcript goes back to the model: your first message, the file it read in turn three, the test output from turn nine, the twelve messages where you both got confused about the same function. Turn forty pays for turns one to thirty-nine.

Three consequences follow, and all three push you toward the new-session button.

- **The bill grows.** Not with the work, with the conversation. An hour of chatting costs more per answer than a minute of it.
- **The window fills.** Eventually something has to be dropped, and what gets dropped is chosen by a rule, not by you.
- **Attention drifts.** A model reading past forty finished tasks to find your question is not in the same position as a model reading one.

If that is the machine you are using, starting fresh is not laziness. It is the rational move.

> The advice to start over is correct for an agent that re-reads its whole conversation every turn. It is just not how this one is built.

## What actually goes to the model

Ogcode does not hand the model a transcript. A request carries three things: the message you just sent, the final answer from the turn before it, so the thread stays connected, and a rolling summary of anything older that still matters. Everything else stays on disk.

That is what makes a long session possible. The size of a turn is set by what the turn does, not by how old the session is. Turn four hundred of a conversation that has been running since last winter costs about what turn four costs, provided both turns read about the same amount.

The mechanics are in [the post about the context window](/blog/context-budget-64k/). The part that matters here is the shape of the curve. For a replay agent, cost climbs with age. For this one, it does not climb at all.

## The session is a file, not a buffer

Nothing in Ogcode deletes a session because it got long.

The transcript is append-only. It is written to disk as the session runs, and it stays. Nothing prunes it, nothing vacuums it, nothing rotates it out at the end of the day, and there is no age at which a session has to be retired.

There is a compressor, but it is a per-turn rescue, not a session reset. When a single turn genuinely reads a lot at once and the request gets too big, the agent summarises what it is about to re-send and carries on. That changes the request for the rest of that turn. It does not touch the stored session, the notes that were written, or the messages you sent. The next turn starts from your message again.

> Compression is a per-turn rescue, not a session reset. It changes what is sent, never what is kept.

So a year-old session is not a conversation that has been squeezed into a paragraph. It is a long file with a short window onto it.

## The part nobody warns you about: a session learns its task

Cost is the argument everyone makes. The more interesting argument is quality.

Every conversation gets its own memory file, written and maintained by the agent as it works. Not a transcript, a set of notes: the decision you made and the reason for it, the approach that failed and why, the invariant that broke the last three attempts, the fact that the tests need a recorded clock, the file that looks relevant and is not. Before it acts on the next turn, the agent reads that file. The prompt carries a pointer to it, not its contents, so the notes can grow for months without costing anything until a turn needs them.

There is a second file, at the project level, for things every future session should know. The split is enforced in the agent's instructions: a fact that only this conversation needs goes in the session file, a fact every future session needs goes in the project's. When something turns out to matter beyond the conversation, it is promoted, and removed from the session file, so it lives in exactly one place. More on that in [how an agent remembers a project](/blog/memory-is-table-stakes/).

Teammates share the file. If a session spawns three subagents to work in parallel, all four write into the parent's notes, because they are all working on the same task.

Now put that next to a fresh session. A new session starts with the project's memory, which is genuinely useful, and none of the session's. It does not know why you rejected the obvious fix on Tuesday. It does not know that the failing test is expected to fail until the migration lands. It will ask you, or worse, guess.

> A fresh session is not a blank slate. It is a senior engineer who has forgotten the project, the decision, and the reason.

The cost of starting over is not only the tokens you spend re-explaining. It is the decisions that get made a second time, differently, by an agent that has lost the argument that settled them.

## So we tell people to use fewer sessions

The advice here is the inverse of the usual one: **one session per task**. Open a session when a task starts. Keep using it while the task lives. A refactor that takes three weeks is one session. A bug that keeps coming back is one session, and the third return is the one where the notes earn their keep. Start a fresh session when the task is genuinely new, not when the conversation feels long.

For a task that spans months, that means a session that spans months.

The obvious objection is the one we had to answer before we could recommend it: if a session runs for a year, how do you find anything in it, or in the forty others you have?

## Finding the session you half remember

You do not remember session IDs. You remember words. The migration that broke on the goose version. The bug where the sidebar jumped when the socket reconnected. The retry logic that needed jitter. Those are the handles people actually have, and they are usually two or three words long.

So session search is a keyword search, in the command palette. Press the shortcut, type two characters, and the palette filters session titles and models locally while it waits for the content search to come back. The results are conversations, not lines: each row is a session with its title, how many turns it has, and the matching snippet highlighted. Press Enter to open it.

The search runs on your machine, against a local index, one project at a time. It is not a chat with your history. It is a finder.

## What gets indexed

The index is built from three things, all of them already written down.

- **One note per turn.** Every turn writes a short markdown note with a title and a line of topic labels: what was asked, what was done, which files and symbols mattered, how it ended.
- **The session's memory file.** The notes about the task, the decisions, the traps.
- **The session's standing summary.** The title and the rolling summary that says what the whole conversation is about.

What is not indexed is the raw transcript. It is the largest thing in the database, most of it is tool output, and the parts that matter have already been written into the notes. Indexing it would make the index bigger than the thing it points at, and fill the results with log lines.

The topic labels are written by a model as the turn ends, not computed when you search. That is where the intelligence sits: a note already says "goose, migrations, duplicate version" because the model that did the work wrote it down. Searching those labels is cheap, precise, and works with no network.

> We index the notes, not the transcript. The notes are what a person would have written on the whiteboard.

## Why no embeddings

This is the decision people ask about, so here it is plainly: no vector store, no embedding model, no chunking strategy, no similarity threshold to tune.

An embedding pipeline would give us "find me something like this". That is a real capability. It is also a second system to run: embed every note as it is written, re-embed everything when the model changes, keep the vectors in step with the markdown, and explain to somebody why the fifth result was judged closer than the second. For "which session did I fix the retry backoff in", it buys nothing, because the words in the query are the words in the notes.

The other half of the argument is how people remember. Someone looking for an old conversation does not arrive with a paragraph to search with. They arrive with a noun: the library, the error, the function, the customer. Keyword search is not a compromise on that query. It is the exact match for it.

We will revisit it when there is evidence that keyword recall is what fails. So far the measurements point the other way.

## Making one word enough

A plain full-text search would have been disappointing, and we measured it before shipping. The first version searched one note at a time and required every word of the query to appear in the same note. Ask about something you discussed across three turns and it returned nothing. On a set of sixty cross-turn questions, nine were found.

The fix was not a smarter model. It was a different definition of a match: **each word may be found in a different note**. A session qualifies if every word appears somewhere in the session, even if no single note contains them all. The same sixty questions went from nine found to fifty-seven.

From there the ranking is mostly common sense.

- **Score the session, not the note.** The best matching note sets the score, so one strong note can carry a conversation.
- **Reward coverage.** A session that mentions every word you typed beats one that mentions half of them.
- **Reward breadth.** A session with eight different matching turns is more likely to be about the subject than one with a single passing mention.
- **Use the labels and the title.** They are short, they are written to describe the subject, and they are the closest thing a conversation has to a headline.
- **Prefer recent, all else equal.** Not because newer is better, but because it is more often what you meant.

One more thing had to give: plurals. Searching for "migrations" found the sessions that said "migrations" and missed the ones that said "migration". The fix is a fallback rather than stemming: if the exact word turns up in very few sessions, the singular is tried as well, at a lower weight. Forced plurals went from 6.7 percent to 75 percent.

<figure class="bp-chart">
  <div class="bp-chart-pan" tabindex="0">
    <img src="/blog/media/session-search-accuracy.svg" alt="Grouped horizontal bar chart, measured: how often the right session ranked first or in the top five, across six kinds of search query on 446 sessions and 1,554 turn notes. First place ranges from 56.8 percent for queries built only from topic labels to 95.0 percent when the answer is spread across several turns. The right session is in the top five at least 86.4 percent of the time, and always for body-word, plural and cross-turn questions." width="960" height="468" loading="lazy">
  </div>
  <figcaption>Measured on 446 sessions and 1,554 turn notes: where the right conversation landed for six kinds of query. The worst case, a query built only from topic labels, still puts the right session in the first five 86 times out of 100.</figcaption>
</figure>

Read the chart as two questions. The orange bar is the one that decides whether search feels like magic: did the right session come first. The blue bar is the one that decides whether search is useful at all: was it on the first screen.

Queries made of body words land first 79.3 percent of the time and in the top five every time. A title alone is nearly as good, at 79.0 and 96.0. The best case is the one a replay agent would find hardest: a question whose answer is spread over several turns, at 95.0 and 100. Even the weakest class, a query built only from the topic labels, is right 56.8 percent of the time and on the first screen 86.4 percent of the time.

Speed is not the interesting number, but it is a good sign. End to end through the API, a search takes 10 to 32 milliseconds on a live index. The first index of fifteen hundred notes took under half a second, in the background, while the application kept working.

## What this does not fix

No post about its own feature should pretend the feature is finished.

**Search is per project.** A conversation from another repository will not turn up. That is deliberate, because results from a different codebase are usually noise, but it does mean the answer to "where did I do this" is "in this project".

**It finds sessions, not answers.** The session you open still has to be read, and the agent still has to recall the detail. Search is the door, not the room.

**Keyword recall fails when you cannot remember a single word.** If all you have is "that thing from a while ago", the query has nothing to match. This is the case where embeddings would genuinely help, and it is the case we are watching.

**A long session still needs good notes.** A session whose memory file is empty is a long conversation with a short memory, and no amount of search fixes that.

## One session per task

The usual advice exists because of a real cost. That cost belongs to a design decision, not to long sessions in general, and the design decision is not compulsory.

A session that does not re-read its history can run for years. A session that writes down what it learned gets better at its own task instead of worse. A search over those notes gets you back to any of them in a few milliseconds. Put the three together and the sensible unit of work stops being the sitting. It becomes the task.

Open a session when the work starts. Keep it while the work is alive. Let it get old.

> The best session for the job is usually the one that has already done half of it.
