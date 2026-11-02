---
title: "Your agent doesn't need RAG to read PDFs"
description: "Hand an agent a 37-page paper and most assistants either load the whole thing into context or bolt a RAG pipeline onto it. Ogcode takes a third path: label every page once, store the labels in a table you can read, and open only the pages a question actually needs."
pubDate: 2026-11-01
tags: ["agents", "pdf", "context-engine"]
hero: "/blog/media/pdfs-one-page-at-a-time-hero.jpg"
heroAlt: "A dark isometric filing cabinet folder filled with page cards; each card tab glows with a short handwritten label, and a small orange agent orb pulls out exactly one page card lit up in brand orange while the rest stay filed."
ogImage: "https://ogcode.in/blog/media/pdfs-one-page-at-a-time-hero.jpg"
---

Picture a research librarian holding a 37-page paper. You ask: where does
this paper report its main benchmark numbers? A good librarian does not
photocopy the whole paper for you. They remember what each page is about,
because they filed it, one page at a time, and they wrote a short label on the
tab of each folder. Page 4: the formal definitions. Page 8: the biggest
results table. They hand you exactly one page. The other 36 stay in the
cabinet.

Now picture an AI agent handed the same paper. What most assistants do is
either photocopy the cabinet or build a machine next to it that guesses which
pages you might want.

This post is about the third path we wired into Ogcode: label every page
once, keep the labels in an ordinary table, and let the agent open exactly
the pages a question needs.

## First, how does an agent usually meet a PDF?

An agent only sees text. For PDFs, most assistants have two moves, and
neither scales:

1. **Extract everything and print it.** The whole file becomes one giant blob
   in the conversation. A long technical report turns into tens of thousands
   of tokens on turn one, and those tokens stay in the context window for the
   rest of the session. On a big enough PDF the blob gets cut instead, and
   the agent answers from whatever part survived the cut. It has no idea
   what fell off.
2. **Bolt on RAG.** Chunk the pages into overlapping fragments, embed every
   fragment into a vector store, and at question time retrieve the top few by
   cosine similarity. It works, and it is the standard answer, but notice
   what it costs you. You now run and maintain a second system. The
   embeddings are opaque: nothing you can print, read, or audit. The model
   answers from fragments, not pages: excerpts with no neighbors, no page
   numbers, and no guarantee you got the page where the sentence you half
   remember actually lives.

Both moves pay a tax on every question, in tokens or in infrastructure,
before anyone has read a single page.

## Ogcode's answer: label every page once, then open only what a question needs

There are two parts, and like the spreadsheet fix, they share one brain: a
one-time labeling pass over the file, and a pair of tools that make the
labels cheap to act on.

### Part one: the labeling pass

Ogcode scans every page of every PDF in your workspace with a plain-text
extractor, collects the keywords that actually occur on each page, and hands
those keyword lists to the model in one small batch. The model returns page
labels: short phrases that say what each page is about. Nothing fancy is
stored. Each page lands as one ordinary row in an ordinary SQLite table,
keyed by file and page number:

```
doc_path                              page_num  labels
demo-pdfs/harness2-arxiv...pdf        8         Table 1 WorkBuddy Bench Results, Macro-Average Score, ...
```

The whole labeling question for our 37-page test file fit in a single call
(pages go in batches of up to 100, and the keyword format keeps each batch's
prompt under 50 KB). No embeddings, no vector store, no retrieval
infrastructure. If you want to audit what the agent knows about your
documents, you can print the whole table. It is a card catalog you can read.

Labels are made by the model, so they are a summary with judgment in them,
not a mechanical keyword dump. That judgment is what makes labels like
"Figure 4 Pipeline Overview" or "References: Reflexion" show up on the right
pages.

The pass is frugal about when it runs. Each file carries its last-modified
time in the index, so nothing is rescanned unless the file actually changed.
You can rebuild with `ogcode index` at any time, and the agent triggers the
same refresh after a completed work turn. The labeler also runs politely: it
shares your provider's connection and yields to your actual requests first.
Indexing your PDFs is a background errand, not a job that competes with the
work you are paying for.

### Part two: the map and the page

Labels are only useful if the agent finds them at the right moment. When the
agent surveys your project (the codebase map it checks before opening
files), each PDF shows up as a single entry with its page labels folded
together, capped at thirty. The agent sees "this is the Harness paper, and it
has results tables, ablations, appendices, and prompt listings" before it has
opened anything.

Then two tools do the page-level work:

- **pdf_index** lists the labels page by page. It never opens the file. It
  reads only the table, which is why it costs almost nothing.
- **read_pdf_page** opens exactly one page and returns its plain text. If
  that page has almost no extractable words (a full-page figure) and the
  model can see, it renders that page to an image at a bounded resolution
  instead, and falls back to text if the image cannot be produced. Either
  way, the unit of reading is the page, with a page number you chose from
  the index.

The pairing is deliberate, and there is a test that keeps it that way: an
agent that jumps straight to reading a PDF page with a page number it had no
way of knowing is failing, and the tool layer refuses to paper over it. The
index and the page have to travel together.

## A real paper, three steps

Our test file: an arxiv preprint about recursively improving agent
harnesses, 37 pages, 8.1 MB. Dense academic text, figures, tables, 4 pages
of references, 2 appendices. Here is the actual session, against the real
index Ogcode built from it.

**Step one: `pdf_index` gives the page-level map.** Straight from the
table, no file opened:

```json
{"page": 7, "labels": ["Harnessing-Level Recursion", "Experiment Setup",
  "Benchmarks: WorkBuddy, Job, Legal Mark", "ReasoningBank Baseline",
  "Gemini Flash Executors", "Claude Opus Harness Editor"]},
{"page": 8, "labels": ["Table 1 WorkBuddy Bench Results",
  "Macro-Average Score", "Single-Step Improvement Gains",
  "Gemini Flash Code Results", "Weaker Executor Larger Gains"]},
{"page": 9, "labels": ["Table 2 Job Bench and Legal Bench Results",
  "Codex Executor Results", "Ablation Study Overview"]}
```

**Step two: ask the question against the labels.** "Where does the paper
report its main benchmark numbers?" The labels answer it before any page is
read: page 8 is Table 1, WorkBuddy Bench. The card catalog alone did the
finding.

**Step three: one page gets read.** `read_pdf_page`, page 8:

```
Harness2:RecursiveAgentHarnessingforanOpenWorld
Table1|WorkBuddy-Benchundertwoopen-sourceinitialharnesses...
ModelsMethodCodeWebOﬃceAvg.
...
Harness276.5(1.3)22.8(1.3)76.4(4.5)17.7(0.7)79.5(3.1)25.1(1.7)77.5(2.5)21.9(0.4)
...
Harness2raisesGLM-5'saveragescoreby11.5pointsunderOpenCode,thelargest
one-stepimprovementoverthebaseharnessonthisbenchmark.
```

That is the honest raw output: a table with its numbers, plus the sentence
stating the headline result. You can also see the extractor's one
imperfection on dense pages: inside-line spacing sometimes collapses, so
"Harness2" reads as one word. For an agent looking for numbers and
sentences to quote, it is plenty. What matters is the shape of the trade:
one page of tokens, on demand, instead of 37 pages of PDF parked in
everyone's context for the whole session.

## The same paper, three harnesses

Numbers, because "fewer tokens" is easy to claim and easier to fudge. Same
paper, the three ways a harness can meet a PDF. Token counts here are
character counts divided by four, the standard back of the envelope: treat
each figure as an estimate, and trust the shape.

A single question on a small paper flatters every harness. Nobody actually
works that way. Real use is a pile of questions against the same file: what
does this table say, where is the ablation, which page defines the metric.
Multi-question sessions are where the three strategies stop pretending to be
equivalent, and where one of them is simply the best.

| | Dump the file | RAG pipeline | Ogcode: labels, then pages |
|---|---|---|---|
| Setup | nothing to build | chunk the text into ~500-token windows, embed all ~60 fragments into a vector store, tune top-k | one background labeling call: the model reads ~20k tokens of page keywords once and returns ~3k tokens of labels |
| What the model sees per question | all 37 pages, ~23,000 tokens, parked in context | the top ~5 fragments (~1,500 tokens), picked by cosine similarity | the label table (the full catalog is ~3,200 tokens; the folded map view is a few hundred) plus one page, ~900 tokens |
| A ten-question session | ~230,000 PDF tokens carried through the window | ~15,000 fragment tokens, plus ten embedding round trips | ~12,000 page tokens, often less: some answers come from labels alone |
| Where the answer lives | whole pages, all of them, all at once | fragments with no neighbors and no page numbers | whole pages you can cite by number |
| How it fails quietly | on a bigger file the blob gets cut, and the model answers from whatever survived | the retriever misses the chunk, and nothing tells you | a wrong label costs exactly one page read to correct |
| Where the cost goes as questions pile up | nowhere good: the blob rides every turn | flat per question, but rebuilt from zero every time | down: after the first two questions the agent knows the map and opens only pages |

One footnote, kept honest: on a first question, RAG and Ogcode spend about
the same number of tokens. Five chunks and one page are the same order of
size, and we will not pretend otherwise. From the second question on, the two
part ways. RAG rebuilds its answer from zero every time: rank the chunks,
re-embed, hand over fragments that carry no memory of the question before.
Ogcode's agent has already read the label map, so a later question usually
costs one page read, and some cost nothing but the labels. The labeling pass
itself costs about as much as the blob would (~23k tokens of keyword input
in, ~3k of labels out), but on a different clock: the blob enters your
working session and rides along every turn, while the labeling call happens
once in the background and never enters the working conversation at all.

![Line chart on a log scale: cumulative PDF tokens entering the model across ten questions on the 37-page paper. Dump climbs from 23,000 to about 230,000. RAG and Ogcode start tied at 1,500, then Ogcode flattens to roughly 12,000 by question ten while RAG reaches 15,000, with an occasional labels-only question pulling it lower.](/blog/media/pdfs-cumulative-ten-questions.svg)

Speed is not close either. The catalog lives in SQLite, so pdf_index never
opens the 8.1 MB file: a label lookup is a database read, effectively
instant, and read_pdf_page renders a single page. No embedding round trip,
no retrieval service to wake up, and the first question costs the same as the
tenth. Against the dump, the win is bigger than tokens: a smaller context is
a faster model. Twenty thousand tokens of figures and references slow down
every answer in the session, whether or not the question touches the paper.

![Bar chart: tokens of PDF entering the model per question, dump 23,000 versus RAG 1,500 versus Ogcode 900, on the 37-page test paper](/blog/media/pdfs-tokens-per-question.svg)

## Now the same test on a book

A 37-page paper is a gentle test. The kind of PDF you actually resent is the
thousand-page one, so we repeated the measurements on a downloaded copy of the
*Dive into Deep Learning* book: 1,151 pages, 44.7 MB, real typeset text. The
same character-count-over-four arithmetic applies, and every number below is
measured on the file.

- **The dump stops existing.** The extracted text is 2.1 million characters,
  about 532,000 tokens. Most context windows cannot hold it, and on the few
  models that stretch that far you would be paying for the whole book on
  every single question. At 37 pages the blob was inadvisable; here it is
  undiscussable.
- **RAG still works, unchanged.** The book chunks into roughly 1,040
  fragments, and a ten-question session still spends about the same ~15,000
  fragment tokens as before. This is the fair point in RAG's favor: per
  question, its token bill barely moves with document size. What grows is the
  blind part, a thousand neighbor-less fragments as the only map of the book.
- **Ogcode's bill per question barely moves either.** A median page of this
  book is about 460 tokens, so an answer costs one or two page reads. The
  label table is the interesting part: at book scale it would be around
  100,000 tokens, far past the point where anyone should paste it anywhere.
  But pasting it was never the plan. It lives in SQLite, the map view folds it
  down to a few hundred tokens, and pdf_index reads from the same database a
  slice at a time. The catalog growing a hundredfold changes nothing that the
  agent actually does.
- **The setup work grows, on the right clock.** The keyword pass feeding the
  labeling sessions is about 400,000 tokens of input here. The indexer never
  sends a document that size in one call: multi-page documents are labeled in
  sessions of 100 pages each, so this book runs as twelve background calls
  while you do something else. Nothing of it enters a working session.

The shape of the result is the point. At 37 pages the argument is "fewer
tokens"; at 1,151 pages it is "fewer tokens versus a file your context cannot
hold". The dump's cost curve ends in a wall. Retrieval's cost curve stays
flat but maps the book with fragments, cold every time. This one stays flat
too, it maps the book with citable pages, and it is the only one that gets
cheaper as the questions add up.

## Which one, then

For a single throwaway question on a short paper, paste the dump. It is
fine, and there is nothing to build. For every other way a PDF gets used,
which is to say more than a couple of questions on the same file, the page
index is the best harness overall: cheapest per answer after a short
warmup, the only approach that still works at book scale, and the only one
whose failure mode is visible instead of silent. RAG's one genuine
advantage, a flat per-question bill, Ogcode matches, without the chunking,
the embeddings, the vector store, or the retriever you have to keep fed.

## What this is not

**Not RAG.** There is no chunking, no embeddings, no vector store, no cosine
similarity, no top-k. The index is a table of short text labels, one row per
page. It takes up a few kilobytes and you can dump it into any database
viewer and read it. Questions are answered by the agent scanning labels and
choosing pages, which is a decision you can watch and audit, not a
retriever's guess.

**Not magic either.** The labels come from the words on each page, so a
scanned PDF with no text layer pages will produce thin, best-guess labels.
They are model-made, so occasionally one is wrong. The honest mitigation is
built into the design: a wrong label costs you exactly one page read to
correct, because the page is always one call away.

## Where this could still be better

- Scanned, image-only PDFs get sparse labels from whatever little text
  exists. An OCR pass ahead of the keyword scan would fix the thin ones.
- The render-and-look fallback only helps models that can see. On a
  text-only model, a page that is nothing but a figure stays invisible.
- A label names a page; it cannot say "this concept spans pages 5 through
  9." The agent infers that itself by reading a few neighboring pages, which
  works, but it is inference, not index.
- The plain-text extractor glues words together on some LaTeX-set PDFs
  ("AssumeTheModel" instead of "Assume the model"). The labels survived it on
  both of our test files, but a cleaner extraction pass would make thin
  labels rarer.

## The idea in one sentence

Read a PDF the way a librarian files one, once, with a label on every page,
and the agent's next question costs one page instead of the whole file.