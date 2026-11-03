---
title: "Your agent does not need the whole context window"
description: "Models lose accuracy long before their window is full, so Ogcode budgets 64K tokens of working context on every model and compacts there instead of waiting for the window to fill."
pubDate: 2026-11-03
tags: ["agents", "context-engine", "long-context", "compaction", "research"]
---

The agent has been working for forty minutes. Its context window says 200,000
tokens. It is carrying 180,000 of them: the first read of a config file that
turned out to be irrelevant, the second read of the same file done properly,
six thousand lines of test output it never used, its own running commentary,
and somewhere near the top, one paragraph from you describing what the task
actually is.

Every step re-sends all of it. Every step is charged for the whole pile and
reasons over the whole pile, and the paragraph that matters is the oldest,
least attended-to text in the window.

Nothing in that picture is a bug in the agent. It is what happens when you
treat the size of the window as a budget. The window is not a budget. It is a
ceiling, and an optimistic one.

## The window you were sold, and the window you get

The number on the model card is a maximum input length. It says the model will
accept that many tokens without falling over. It says nothing about whether the
model will still be good at that length, and a lot of published evaluation work
says it will not.

Start with position, which is the cheapest effect to see. Liu et al., in "Lost
in the Middle: How Language Models Use Long Contexts" (TACL 2024,
[arXiv:2307.03172](https://arxiv.org/abs/2307.03172)), measured a U-shaped
curve: models are strong when the evidence is first, weak when it sits in the
middle, and strong again when it is last. On multi-document QA, moving the one
passage holding the answer from position 1 to position 10 dropped GPT-3.5-Turbo
from 75.8% to 53.8%, which is below its own closed-book score, at a total
context of only a few thousand tokens.

Then volume, with the content held fixed. Levy et al., "Same Task, More Tokens"
(ACL 2024, [arXiv:2402.14848](https://arxiv.org/abs/2402.14848)), gave models
the identical task and only made the input longer. Accuracy fell from 0.92 to
0.68 by roughly 3,000 tokens of input. Not 300,000. Three thousand.

That gap between the advertised window and the usable one now has a name in the
literature. RULER ([arXiv:2404.06654](https://arxiv.org/abs/2404.06654)) found
that the effective context length of most models sits far below the advertised
one, and that only a handful handled what they claimed. NoLiMa
([arXiv:2502.05167v1](https://arxiv.org/html/2502.05167v1)) made the needles
harder to match by keyword, and effective context lengths collapsed to
2K to 16K tokens.

The most uncomfortable version of the finding is about retrieval. Du et al.,
Findings of EMNLP 2025
([2025.findings-emnlp.1264](https://aclanthology.org/2025.findings-emnlp.1264.pdf)),
tested a model advertising 128K and found quality falling from around 30K
tokens, and falling even with perfect retrieval: the evidence was provably in
the prompt, 970 of 1,000 exact matches. When the passage is definitely there
and the answer is still wrong, the problem is not search. It is attention.

Two engineering-flavoured measurements say the same thing in numbers rather
than percentages. Databricks ran over 2,000 runs and reported in
["Long Context RAG Performance of LLMs"](https://www.databricks.com/blog/long-context-rag-performance-llms)
that GPT-4-0125 stopped improving past about 64K tokens and Llama-3.1-405B past
about 32K. Past those points more context stopped paying for itself. Chroma's
["Context Rot"](https://research.trychroma.com/context-rot) swept 18 models from
10K to 100K and saw quality degrade as input grew, with the clearest knee
between 32K and 64K. TMLS, in ["Context rot: a mechanistic
view"](https://tmls.nyc/research/context-rot-mechanistic), tested 12 models on
an association benchmark and found 10 of 12 below half their short-context
score by 32K tokens.

Look at where those knees land: 32K to 64K. Not 180K. Not "the last inch of the
window". The cliff is a small fraction of what the model was sold as.

## More context is not free

Three bills arrive together, and only the first one shows up on an invoice.

The first is per-step cost. A conversation is re-sent every step, so a window
that carries 180,000 tokens of history charges for 180,000 tokens on every
tool call, every retry, every small correction. You are not paying once for
reading a file. You are paying for it on every step until the turn ends.

The second is attention. A transformer spreads its attention over what it is
given. Everything in the window competes with the question, including the two
hundred lines of stack trace that turned out to be a red herring. You cannot
add material to a prompt without changing what the model pays attention to.

The third is position, which is the one people forget. A fact at the top of a
64,000-token context is not worth what the same fact is worth adjacent to the
question. Shrinking the window helps, but only if the shrinking keeps the
question and the evidence near each other.

There is also a tempting shortcut to avoid: compression with no idea what the
question is. LongLLMLingua
([arXiv:2310.06839](https://arxiv.org/abs/2310.06839)) reported a 21.4% gain on
NaturalQuestions with roughly 4x fewer tokens, but the gain came from
question-aware compression. Question-agnostic compression made results worse.
Cutting tokens blindly is the documented failure mode, not the fix.

## What Ogcode does now

The change is small to state. Ogcode now caps the working context at 64,000
tokens and compacts there, on every model, instead of waiting until the window
is nearly full.

The trigger is computed from the model's own window, because a 32K local model
and a 1M hosted model cannot share one number:

```go
reserve := min(20000, contextWindow/2)                 // response room + margin
compactionThresholdTokens := min(contextWindow-reserve, 64000)
```

The 20,000-token reserve is held back for the response and a safety margin, and
it is clamped to at most half the window so a small local model still gets a
usable budget of its own. The 64,000 is the default ceiling, and it is
overridable with `OGCODE_COMPACTION_THRESHOLD_TOKENS`.

Before this, the threshold was simply `contextWindow - reserve`. A 200K model
ran to about 180K before anything asked it to compact, and by then the useful
part of the conversation was already buried.

Compaction itself is not a hard chop. The loop asks the model to summarize the
older part of the turn, keeps the most recent messages verbatim (`keepRecent =
clamp(contextWindow / 10000, 8, 40)` messages), and folds any earlier summary
into the new one. Repeated compaction therefore accumulates rather than
forgetting: what the first summary captured is still present in the third.

There are two thresholds, not one, and they do different jobs.

| Signal | Default | Override |
| --- | --- | --- |
| Read-pressure reminder | 40,000 tokens of tool output | `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS` |
| Proactive compaction | 64,000 tokens of context | `OGCODE_COMPACTION_THRESHOLD_TOKENS` |

At 40,000 tokens of accumulated tool output the agent gets a reminder that it
has used a lot and should compact what it no longer needs. At 64,000 the loop
stops asking and does it. One is a suggestion, the other is a policy.

The compaction budget per run also had to scale, or the ceiling would quietly
undo itself. It is no longer a flat two per run:

```go
maxCompactions := clamp(contextWindow/64000, 2, 6)
```

A 128K model still gets 2, a 200K model gets 3, a 1M model gets 6. Without
that, a long turn would exhaust its allowance early and then spend the rest of
its life running at the old, nearly-full-window threshold, which is exactly the
behaviour the ceiling exists to prevent.

One more piece in the loop is unglamorous and necessary. After a compaction
the loop advances a watermark, so the next step resumes from the summary plus
the recent tail instead of rebuilding the full turn history and discovering it
is over the threshold again. Skip that and you compact once per step for the
remainder of the run.

Finally, the same threshold drives the context-used meter in the UI. The
exported `CompactionThreshold` feeds the model list, so the number the user
watches and the number the loop acts on come from one place. They cannot drift
apart.

## What 64K is not

It is a ceiling, not an optimum, and not a law of nature. The measured knees
cluster at 32K to 64K, which makes 64K the point past which you are
unambiguously spending money on tokens the model demonstrably uses badly. Below
it there is a wide band where more context is still often a good trade, and a
task that genuinely needs 50,000 tokens of source file should get them.

Compaction is lossy. A summary is not the transcript, and no amount of prompt
engineering makes it one. The mitigation is not a better summarizer; it is that
durable facts belong on disk. Ogcode keeps them in `MEMORY.md` and in per-turn
notes under `.ogcode/memory/`, so the things that must survive a session are
not living in the one place that is designed to be thrown away and rewritten.
A window is working memory. Treating it as storage is how you lose things.

And position still matters as much as volume. Evidence sitting at the top of a
64K context is worth less than the same evidence next to the question. A
ceiling does not fix ordering, and no threshold will. It only stops the worst
case from getting worse.

## The rule

Budget for the evidence a turn actually needs, not for the size of the window
you were sold. Keep the question next to the evidence that answers it. And keep
whatever must survive outside the window, in a file, where a summary cannot
erase it.
