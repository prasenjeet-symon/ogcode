---
title: "Your agent doesn't need RAG to read PDFs"
description: "Hand an agent a 1,151-page book and most assistants either load the whole thing into context or bolt a RAG pipeline onto it. Ogcode takes a third path: label every page once, store the labels in a table you can read, and open only the pages a question actually needs."
pubDate: 2026-11-01
tags: ["agents", "pdf", "context-engine"]
hero: "/blog/media/pdfs-one-page-at-a-time-hero.jpg"
heroAlt: "A dark isometric filing cabinet folder filled with page cards; each card tab glows with a short handwritten label, and a small orange agent orb pulls out exactly one page card lit up in brand orange while the rest stay filed."
ogImage: "https://ogcode.in/blog/media/pdfs-one-page-at-a-time-hero.jpg"
---

Picture a research librarian holding a 1,151-page book. You ask: where does
this book explain the Transformer architecture? A good librarian does not
photocopy the whole book for you. They remember what each page is about,
because they filed it, one page at a time, and they wrote a short label on the
tab of each folder. Page 14: the chapter contents for attention and
transformers. They hand you exactly one page. The other 1,150 stay in the
cabinet.

Now picture an AI agent handed the same book. What most assistants do is
either photocopy the cabinet or build a machine next to it that guesses which
pages you might want.

This post is about the third path we wired into Ogcode: label every page
once, keep the labels in an ordinary table, and let the agent open exactly
the pages a question needs.

## First, how does an agent usually meet a PDF?

An agent only sees text. For PDFs, most assistants have two moves, and
neither scales:

1. **Extract everything and print it.** The whole file becomes one giant blob
   in the conversation. A real technical book turns into hundreds of
   thousands of tokens on turn one, and those tokens stay in the context
   window for the rest of the session. On a big enough PDF the blob does not
   even fit, and the part that gets cut is whatever the extractor hit first.
   The agent has no idea what fell off, and on a book, most of the book fell
   off.
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
those keyword lists to the model in small batches. The model returns page
labels: short phrases that say what each page is about. Nothing fancy is
stored. Each page lands as one ordinary row in an ordinary SQLite table,
keyed by file and page number:

```
doc_path                              page_num  labels
demo-pdfs/d2l-en-book.pdf             14        Transformer Architecture, Positional Encoding, Vision Transformers, ...
```

The whole labeling job for our 1,151-page test file ran as twelve background
sessions (pages go in batches of up to 100, and the keyword format keeps each
batch's prompt under 50 KB). No embeddings, no vector store, no retrieval
infrastructure. If you want to audit what the agent knows about your
documents, you can print the whole table. It is a card catalog you can read.

Labels are made by the model, so they are a summary with judgment in them,
not a mechanical keyword dump. That judgment is what makes labels like
"Training Transformers" or "Positional Encoding Details" show up on the right
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
together, capped at thirty. The agent sees "this is the Dive into Deep
Learning book, and it covers CNNs, RNNs, attention, optimization, GANs"
before it has opened anything.

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

## A real book, three steps

Our test file: *Dive into Deep Learning*, the full printed book as a single
PDF. 1,151 pages, 44.7 MB, 2.1 million characters of extracted text. Real
typeset text, equations, figures, code listings, and a forty-page detailed
table of contents up front. Here is the actual session, against the real
index Ogcode built from it.

**Step one: `pdf_index` gives the page-level map.** Straight from the
table, no file opened (labels trimmed for fit):

```json
{"page": 13, "labels": ["Attention Mechanisms and Transformers",
  "Queries Keys and Values", "Scaled Dot-Product Attention",
  "Multi-Head Attention", "Self-Attention", "Positional Encoding"]},
{"page": 14, "labels": ["Positional Encoding Details",
  "Transformer Architecture", "Transformer Encoder",
  "Transformer Decoder", "Training Transformers",
  "Vision Transformers (ViT)"]}
```

**Step two: ask the question against the labels.** "Where does the book walk
through the Transformer architecture, and how does it train one?" The labels
answer it before any page is read: page 14 carries the whole Transformer
cluster: model, encoder, decoder, training. The card catalog alone did the
finding.

**Step three: one page gets read.** `read_pdf_page`, page 14:

```
11.6.3PositionalEncoding
437
11.7TheTransformerArchitecture
440
11.7.1Model
441
11.7.2PositionwiseFeed-ForwardNetworks
442
11.7.3ResidualConnectionandLayerNormalization
443
11.7.4Encoder
444
11.7.5Decoder
445
11.7.6Training
447
11.8TransformersforVision
451
```

That is the honest raw output: the exact section numbers and page numbers,
440 to 451, where the book defines, implements, and trains the transformer.
You can also see the extractor's one imperfection on dense pages: inside-line
spacing sometimes collapses, so "PositionwiseFeed-ForwardNetworks" reads as
one word. For an agent that wanted to know where, it is plenty: it now reads
page 450 next, because that is where the training loop lives. What matters is
the shape of the trade: one page of tokens, on demand, instead of 1,151 pages
of PDF parked in everyone's context for the whole session.

## The same book, three harnesses

Numbers, because "fewer tokens" is easy to claim and easier to fudge. Same
book, the three ways a harness can meet a PDF. Token counts here are
character counts divided by four, the standard back of the envelope: treat
each figure as an estimate, and trust the shape.

A single question on a small file flatters every harness. Nobody actually
works that way. Real use is a pile of questions against the same file: what
does this section say, where is the derivation, which page defines the
metric. Multi-question sessions are where the three strategies stop
pretending to be equivalent, and where one of them is simply the best.

| Dimension | Dump the file | RAG pipeline | Ogcode: labels, then pages |
|---|---|---|---|
| Setup | nothing to build | chunk the text into ~500-token windows, embed all ~1,040 fragments into a vector store, tune top-k | one background labeling job: the model reads ~400k tokens of page keywords across twelve sessions and returns ~100k tokens of labels |
| What the model sees per question | all 1,151 pages, ~532,000 tokens, more than most context windows will accept | the top ~5 fragments (~1,500 tokens), picked by cosine similarity | the label table (the full catalog is ~100,000 tokens; the folded map view is a few hundred) plus one page, ~460 tokens |
| A ten-question session | ~5,300,000 PDF tokens carried through the window | ~15,000 fragment tokens, plus ten embedding round trips | ~13,000 page tokens, measured in the gauntlet below; some answers cost only the labels |
| Where the answer lives | whole pages, all of them, all at once | fragments with no neighbors and no page numbers | whole pages you can cite by number |
| How it fails quietly | the blob gets cut, and the model answers from whatever survived the cut | the retriever misses the chunk, and nothing tells you | a wrong label costs exactly one page read to correct |
| Where the cost goes as questions pile up | nowhere good: the blob rides every turn | flat per question, but rebuilt from zero every time | down: after the first two questions the agent knows the map and opens only pages |

One footnote, kept honest: on a first question, RAG and Ogcode spend about
the same number of tokens. Five chunks and a page or two are the same order
of size, and we will not pretend otherwise. From the second question on, the
two part ways. RAG rebuilds its answer from zero every time: rank the chunks,
re-embed, hand over fragments that carry no memory of the question before.
Ogcode's agent has already read the label map, so a later question usually
costs one page read, and some cost nothing but the labels. The labeling job
itself costs more than any single question would (~400k tokens of keyword
input, ~100k of labels out), but on a different clock: the blob enters your
working session and rides along every turn, while the labeling job happens
once in the background, split across twelve sessions, and never enters the
working conversation at all. Amortized over every question this file will
ever get asked, it is the cheapest line in the table.

![Line chart on a log scale: cumulative PDF tokens entering the model across ten questions on the 1,151-page book. Dump climbs from 532,000 to about 5,300,000. RAG and Ogcode start tied at 1,500, then Ogcode flattens to about 13,000 by question ten (measured in the gauntlet below) while RAG reaches 15,000, with an occasional labels-only question pulling it lower.](/blog/media/pdfs-cumulative-ten-questions.svg?v=2)

Speed is not close either. The catalog lives in SQLite, so pdf_index never
opens the 44.7 MB file: a label lookup is a database read, effectively
instant, and read_pdf_page decodes a single page. No embedding round trip,
no retrieval service to wake up, and the first question costs the same as the
tenth. Against the dump, the win is bigger than tokens: a smaller context is
a faster model. Five hundred thousand tokens of book slow down every answer
in the session, on the models that can hold it at all, whether or not the
question touches the book.

![Bar chart: tokens of PDF entering the model per question, dump 532,000 versus RAG 1,500 versus Ogcode 900, on the 1,151-page test book](/blog/media/pdfs-tokens-per-question.svg?v=2)

The shape of the result is the point. When the file fits in your window, the
argument is "fewer tokens"; at 1,151 pages it is "fewer tokens versus a file
your context cannot hold". The dump's cost curve ends in a wall. Retrieval's
cost curve stays flat but maps the book with fragments, cold every time. This
one stays flat too, it maps the book with citable pages, and it is the only
one that gets cheaper as the questions add up.

## Ten questions, asked, answered, and measured

Estimates are cheap, and the Ogcode row in the table above is still arithmetic on a page count. So we ran it for real: ten straight questions, eight on the deep learning book and two on *Mathematics for Machine Learning*, answered through the actual flow. Labels first, then only the pages the labels point at, with every page recorded. Tokens are extracted characters divided by four, the same convention as before, and page numbers are the PDF's own numbering.

| Question asked | Pages opened | Page tokens |
|---|---|---|
| **d2l** · Derive the attention pooling estimator (Nadaraya-Watson) and say where it lives. → A weighted average over kernel similarities (boxcar, Gaussian, Epanechnikov); the from-scratch code normalizes kernel weights and averages training labels. | 455–457 | ≈1,600 |
| **d2l** · Why does Xavier initialization pick the variance it does? → Forward and backward stability cannot both hold exactly; the book splits the difference at σ² = 2/(n_in + n_out). | 227–228 | ≈1,325 |
| **d2l** · What separates overfitting from underfitting, and how do you diagnose it? → The training-vs-validation gap, read against model complexity; overfitting "is not always a bad thing". | 154–155 | ≈1,550 |
| **d2l** · Show the GRU implemented from scratch. → Three gate weight triples, blending H = Z⊙H + (1−Z)⊙H̃, then the concise nn.GRU version. | 419–420 | ≈900 |
| **d2l** · Why do residual connections help deep networks train? → Nested function classes: learning the identity is trivial, so a deeper model never has to do worse. | 342–344 | ≈1,750 |
| **d2l** · Which image augmentations does the book demonstrate, and where? → Horizontal and vertical flip, resized crop (10–100% of area, ratio 0.5–2), color jitter, on CIFAR-10. | 633–634 | ≈900 |
| **d2l** · Softmax regression and cross-entropy: what is the key gradient? → ∂l/∂oⱼ = softmax(o)ⱼ − yⱼ, the same shape as linear regression. | 169–170 | ≈1,425 |
| **d2l** · What does the book argue for moment-based optimizers over plain SGD? → A leaky average replaces the instantaneous gradient; Adam adds a second moment; Yogi when variance is high. | 550, 573, 576 | ≈1,550 |
| **mml** · Where is the SVD defined, and what is it used for? → Theorem 4.22, A = UΣVᵀ, which "applies to all matrices"; used for low-rank approximation, compression, topic modeling. | 125, 135 | ≈1,050 |
| **mml** · Where is the Gaussian defined, and what does the section emphasize? → Univariate and multivariate densities; marginals and conditionals of Gaussians are Gaussians. | 203–204 | ≈995 |
| **All ten questions** | **23 page opens** | **≈13,000** |

That is the whole context bill for ten substantive questions about two books: about 13,000 tokens of PDF text, against 532,000 per question for the dump and about 1,500 plus an embedding call per question for the RAG pipeline. It also lands a rounding error away from the ~12,000 the table estimated, which is the point of measuring: the estimate was arithmetic, and the arithmetic held.

Accuracy held too. Question two is a fair sample of the rest. Asked why Xavier initialization chooses the variance it does, the agent read the labels, opened pages 227 and 228, and came back with the real argument: keeping activation variance stable in the forward pass wants n_in·σ² = 1, keeping gradient variance stable in the backward pass wants n_out·σ² = 1, and both cannot hold at once, so the book splits the difference at σ² = 2/(n_in + n_out). Then the book's own closing line: "This is the reasoning underlying the now-standard and practically beneficial Xavier initialization." Two pages, no retrieval infrastructure, and a citation you can check by turning to the page.

Across the run, twenty-three pages were opened and not one of them was a miss. That is the honest version of the trade the labels make: they steer, the page proves, and when a label is wrong it costs exactly one page read to find out, which is the failure mode you can watch.

One wrinkle worth keeping in view: pdf_index reports a file's whole catalog in one response, and on book-sized indexes that response trims itself to fit its output budget. Both runs hit the trim, on 1,151 pages and again on 417. On the smaller book, one question's labels sat past the trim, and the agent still landed on the exact section by calibrating printed page numbers against a page it had opened. Nothing was lost, because the catalog is an ordinary table the agent can read any slice of, but a paging parameter would let the tool tell the whole story on its own. It is on the list below.

![Bar chart, measured: PDF page tokens opened per question across ten questions, eight on the 1,151-page deep learning book (solid bars) and two on the 417-page Mathematics for Machine Learning book (faded). Every bar sits between 900 and 1,750 tokens on an axis ending at 2,000; the whole-file dump would be 532,000 tokens per question, about 300 times past the right edge. Ten-question total: about 13,000 tokens.](/blog/media/pdfs-qa-gauntlet.svg)

## Which one, then

For a single throwaway question on a file small enough to paste, paste the
dump. It is fine, and there is nothing to build. For every other way a PDF
gets used, which is to say more than a couple of questions on the same file,
the page index is the best harness overall: cheapest per answer after a short
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
- A label names a page; it cannot say "this concept spans pages 440 through
  451." The agent infers that itself by reading a few neighboring pages,
  which works, but it is inference, not index.
- The plain-text extractor glues words together on some LaTeX-set PDFs
  ("AssumeTheModel" instead of "Assume the model"). The labels survived it
  on our test book, but a cleaner extraction pass would make thin labels
  rarer.
- `pdf_index` reports a file's catalog in a single response. On book-sized
  indexes that response trims itself to fit its output budget, so a paging
  or query parameter would let the tool show long catalogs in full.

## The idea in one sentence

Read a PDF the way a librarian files one, once, with a label on every page,
and the agent's next question costs one page instead of the whole file.