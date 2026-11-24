---
title: "3x Fewer Tokens Every Turn: Why the Last Message Lost"
description: "Every turn needs a little of the turn before it. We shipped two ways to supply that, replayed both against 43 real sessions, and the written summary came back at about a third of the tokens, with a bounded tail and more of the task inside it."
pubDate: 2026-11-24
tags: ["agents", "session-memory", "context-engine", "evaluation"]
hero: "/blog/media/last-message-not-memory-hero.jpg"
heroAlt: "A dark isometric desk. On the left sits a short, tidy stack of orange cards. On the right, a tall messy pile of grey cards spills off the edge."
ogImage: "https://ogcode.in/blog/media/last-message-not-memory-hero.jpg"
---

A coding agent works one turn at a time. You send a message, it reads, edits, runs the tests, and answers. Then the turn ends, and the next one begins from your next message.

Something has to connect them. The request for a turn cannot carry the whole conversation, which is the design we wrote about in [the context window post](/blog/context-budget-64k/), so it carries a small, deliberate selection instead: what the agent needs to know about the turn before it. Call it the continuity block. It sits on every request for the life of the session, which makes it the one line item that repeats. Choose it badly and you pay for the choice on every turn.

Our first version was the obvious one. Take the last message the agent sent, and put it in front of the next request, verbatim. It costs nothing to produce, it is exactly what the agent said, and it is the thing a person would scroll up to reread.

Then we replaced it with a short summary written when each turn ends. This post is the measurement that settled which one stays. It uses the project's own history: 43 sessions that had both artifacts, each block rebuilt exactly as its mechanism would have sent it, no model calls and no invented prompts.

**In short:** the summary block is cheaper in 33 of 43 sessions. Its median is 290 tokens against 648 for the last message, its worst case is 625 against 3,264, and across all 43 sessions it comes to about a third of the tokens. The last message wins only when it has almost nothing to say.

## Why a turn needs a memory at all

In Ogcode, a session is not a buffer that gets replayed. The transcript is written to disk, the request is assembled fresh each turn, and the conversation is never pasted back into it. A turn that started completely cold would be an agent that has forgotten what it was doing: it would ask which file you meant, reopen something it already read, or cheerfully re-propose the approach it abandoned twenty minutes ago.

The continuity block is what stops turn forty from re-litigating turn thirty-nine. It is not the transcript, it is not the notes file that the agent maintains for the task, and it is not a search result. It is the short piece of the recent past that goes in front of every request, chosen once per turn, paid for once per turn.

That is also why its size matters more than it looks. A block that costs an extra three hundred tokens costs them again on every turn that follows, for as long as the session lives.

## The two candidates

### The previous message, verbatim

Copy what the agent said last. It has the virtues of a copy: perfect fidelity, no synthesis step, nothing to get wrong. It is also the block we shipped first.

The problem is what a final message is for. It was written for you, after the work had settled, to explain what changed while you were waiting. So it is shaped like a delivery: a conclusion, the handful of details that matter to a reader, and often an offer or a question at the end, which is close to the last thing the next turn needs. It also covers the smallest possible window of the past, the final statement of one turn, not the turn.

And its size is nobody's decision. A final message is one line when the answer is "done" and several thousand tokens when the agent has to hand you a diff, a stack trace, or the contents of a new migration. Whatever it was, the next turn sends it again, and the turn after that too.

### The summary

When a turn ends, we write three to five sentences about it: what was asked, what was done, what broke, what is still open. The next turn reads the last couple of these.

Two things follow from writing it rather than copying it. The first is that the size is chosen: a summary has a shape, and the shape has a length. The second is that it is written for the next turn instead of for you. It can say "the retry test is expected to fail until the migration lands", because that is the fact the next turn needs, and it does not have to say "want me to also update the changelog?".

> A final message is written for the person waiting. A summary is written for the turn that comes next.

## How we measured it

Both candidates already exist in the log. Every turn that ends writes a summary, and every turn leaves a final message. So the comparison needs no fixtures and no live runs: for each session that has both, rebuild the two blocks exactly as production would send them, and count the tokens in each.

The token count uses the same estimator the request path uses when it decides how much it can afford, so the numbers are the ones each request would carry. The whole replay runs in well under a second and can be pointed at any future version of the log.

<figure class="bp-chart">
  <div class="bp-chart-pan" tabindex="0">
    <img src="/blog/media/continuity-injection-cost.svg" alt="Grouped horizontal bar chart, measured: tokens per continuity block over 43 real sessions, on a logarithmic scale. The written summary ranges from 72 tokens at best to 625 at worst, with a median of 290. The previous message verbatim ranges from 8 tokens at best to 3,264 at worst, with a median of 648. Totals across the 43 sessions are 12,980 tokens against 39,932." width="960" height="486" loading="lazy">
  </div>
  <figcaption>One block per session, measured on the project's own history. The orange bars are a size somebody chose. The blue ones are a size that happened.</figcaption>
</figure>

Here are the numbers in a table, because percentiles are easier to argue with than bars.

| Tokens per continuity block | Written summary | Previous message |
|---|---|---|
| Best case | 72 | 8 |
| p25 | 231 | 305 |
| **Median** | **290** | **648** |
| p75 | 345 | 1,616 |
| p90 | 448 | 2,096 |
| Worst case | 625 | 3,264 |
| Total, 43 sessions | 12,980 | 39,932 |

In tokens, the two medians are 290 against 648 per turn. Neither block breaks anyone on its own, and that is not the point. The point is that it is the same ratio every turn, for the whole session.

<figure class="bp-chart">
  <div class="bp-chart-pan" tabindex="0">
    <img src="/blog/media/continuity-tokens-saved.svg" alt="Two vertical bars on a linear scale: total tokens across 43 real sessions. Turn summaries total 12,980 tokens; the previous message verbatim totals 39,932, about 3.1 times as many. The 26,952-token difference stays out of the request." width="960" height="440" loading="lazy">
  </div>
  <figcaption>Added up across all 43 sessions: 12,980 tokens against 39,932. The 26,952-token gap is what stays out of the request.</figcaption>
</figure>

## The median is not the interesting number

Read the two distributions as different kinds of object.

The summary is bounded. Two summaries of three to five sentences each land somewhere in the low hundreds of tokens, and the floor is high enough that it never disappears. When synthesis fails we fall back to a truncated digest, which is also bounded, so there is no path through this mechanism that sends a surprise.

The previous message is unbounded, because the message was never written with a budget in mind. The top quarter of sessions sends more than 1,600 tokens, which is more than the summary's worst case. The top tenth sends more than 2,000. The worst session sends 3,264, about five times the summary's worst.

That shape is what you want from a recurring token cost. A distribution with a tight body and a capped tail means the worst session costs about what the best one does in tokens. A distribution with an open tail means the token total is set by whichever turn happened to produce the longest answer, and you find out after the fact.

> One block is a size that was chosen. The other is a size that happened.

## Where the last message wins, and why that is fine

It won in 10 of the 43 sessions, and in all ten the previous message was short. "Done." "Fixed, tests green." A one-line confirmation of something you already knew.

In those cases a summary is genuinely longer than the thing it summarizes, because a summary has a fixed shape: it names the request, the work, and the outcome even when the outcome is one word. Losing to a one-liner is not a defect to hide. It is the cost of a format that does not shrink to nothing.

It is also the cheapest kind of loss. It is bounded by a few hundred tokens, it only happens on turns that did almost nothing, and it buys the bounded worst case on every other turn. The failure that would worry us is the opposite one: paying multiples of the tokens precisely on the turns that did the most work.

## What the summary knows that the message does not

The final message is the last event of a turn, not the turn. Before it, the agent read files, tried something, watched a test fail, changed direction. The final message usually reports the settled outcome and leaves out the settling: which approach was rejected and why, which test is expected to fail until the migration lands, which file looked relevant and was not.

The summary is written across the whole turn, at the moment it ends, with all of that still in view. That is the part a person would have written on the whiteboard, and it is the part the next turn needs. So the smaller block is not a lossy copy of the bigger one. It is a different compression, of more material, aimed at the next reader rather than the last one.

> The last message tells you how the turn ended. The summary tells you what the turn was.

## So we deleted the other path

The working tree now has one continuity mechanism instead of two. The verbatim path is gone, its tests with it, and the summary block is what a turn gets.

That is the outcome the measurement asked for, but the maintenance argument landed first. Two mechanisms meant two code paths, two sets of tests, and a policy question every time the request is assembled: which one does this turn get. The replay made that question easy to answer, and once it was answered there was no reason to keep the loser around.

The cost we accept is the one the summary always had. A summary has to be written, so there is a short output at the end of every turn, and the next turn waits briefly for it, with a fallback if it is not ready in time. That work is done in the background and amortized across every later turn that reads the block.

## What this does not measure

**Injection only.** The numbers above are the block that goes into the request, not the tokens spent writing the summary. Synthesis is a short output paid once per turn and then read many times, so counting it would need a per-session model of how many turns follow. We report the part that repeats, because that is the part that compounds.

**Tokens, not quality.** We did not grade answers. The argument that a summary carries a task forward better than the last message is an argument from what each contains, not a measured accuracy number. It would be a good thing to measure next, and it is a harder measurement than this one.

**One project's history.** Forty-three sessions of this project's own work, replayed statically against the log. A different agent with different turn shapes could land somewhere else, which is why the replay is a script over the log rather than a table in a post. It runs again against whatever the next hundred sessions look like.

## The last message is not the memory

It is the receipt: itemized for the person who was waiting, issued when the turn closed, and silent about how the work got done. The memory is the shorter thing written afterward, for whoever comes next, with a length somebody chose.

Keep the receipt. Send the memory.

> The last message is not the memory. It is the receipt.
