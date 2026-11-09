---
title: "Why Corporate .docx Files Break AI Agents"
description: "A .docx is a zip of XML with no pages in it. Ogcode reads Word documents (.docx, .docm, .dotx) directly, with no Word, LibreOffice or conversion service: it resolves heading styles, extracts tables, footnotes and text boxes, groups the text into deterministic pseudo-pages, notices when the index has gone stale, and opens one page or a run of pages at a time with checkable citations."
pubDate: 2026-11-06
updatedDate: 2026-11-09
tags: ["agents", "docx", "context-engine"]
hero: "/blog/media/docx-without-word-hero.jpg"
heroAlt: "A dark desk seen from above: a grid of blank frosted tiles glowing faintly orange, with one tile lifted clear of the grid by a small orange orb."
ogImage: "https://ogcode.in/blog/media/docx-without-word-hero.jpg"
---

Word documents are where work actually lives. The specification, the vendor agreement, the memo from legal, the architecture decision record, the onboarding guide nobody has edited since 2021. Ask an agent to read one, and one of three things usually happens: it shells out to LibreOffice or pandoc or `python-docx`, it sends the file to a cloud conversion service and waits, or it reads the filename and invents the contents.

All three treat the format as an opaque black box that needs a heavyweight translation engine. It isn't. A Word document in the Open Packaging Convention format is a standard ZIP file full of XML, and the XML is mostly structured paragraphs.

We previously wrote about [reading PDFs one page at a time](/blog/pdfs-one-page-at-a-time). This is the same architectural pattern applied to the document format that dominates corporate workflows. The mechanics differ in one fundamental respect: a PDF has explicit physical pages pre-rendered by a layout engine. A Word document has no pages at all.

**In short:** a Word file has no pages, so an agent that needs to cite one has to invent them. The trick is to invent them the same way every time: respect real page breaks, cut where a heading makes a clean break, label each page once in a table, and open only the page the question needs. This post walks through how Ogcode does that, including what changed since the first version of this post.

## A .docx is a ZIP

Rename `quarterly-review.docx` to `quarterly-review.zip`, run `unzip -l`, and the mystery disappears:

```
Archive:  quarterly-review.docx
  Length      Date    Time    Name
---------  ---------- -----   ----
     1351  1980-00-00 00:00   [Content_Types].xml
      590  1980-00-00 00:00   _rels/.rels
     1892  1980-00-00 00:00   word/_rels/document.xml.rels
   182410  1980-00-00 00:00   word/document.xml
    32104  1980-00-00 00:00   word/styles.xml
     8421  1980-00-00 00:00   word/footnotes.xml
     4102  1980-00-00 00:00   word/endnotes.xml
```

> A .docx is not a document. It is a zip file that describes one.

The standard wraps an entire document ecosystem into one container. Ogcode's indexer accepts the three OOXML word-processing containers:

- **`.docx`**: standard Word documents.
- **`.docm`**: macro-enabled Word documents. The underlying XML is identical; the macro payload lives in a separate binary part.
- **`.dotx`**: Word document templates.

*(Legacy binary `.doc` files from Word 97 to 2003 are a different format entirely. They are not ZIP archives, and the reader says so in plain language: convert the file to `.docx` first. A ZIP error would only send the agent hunting for the wrong problem.)*

To read any of these, Ogcode needs no Cgo bindings, no Python runtime and no external command-line utility. The Go standard library opens the archive and streams the XML, one token at a time.

Only `word/document.xml` is mandatory. Styles, footnotes and endnotes are read when present. If one of those parts is missing or broken, extraction continues: a custom heading simply goes unflagged, or a note body is missing while its `[^1]` marker stays where it was.

## Streaming the XML

A production Word document can contain hundreds of thousands of XML tokens. Building the whole tree in memory wastes allocations and time. Ogcode uses a streaming tokenizer instead:

```xml
<w:p>
  <w:pPr>
    <w:pStyle w:val="Heading1"/>
    <w:pageBreakBefore/>
  </w:pPr>
  <w:r>
    <w:t>System Architecture</w:t>
  </w:r>
</w:p>
```

The extractor handles the structural parts of WordprocessingML that carry meaning:

- **Paragraphs and runs:** `<w:p>` begins a paragraph, `<w:r>` is a character run with shared formatting, and `<w:t>` carries literal text.
- **Breaks and tabs:** an explicit `<w:br w:type="page"/>` forces a page boundary. Soft breaks (`<w:br/>`), carriage returns (`<w:cr/>`) and Word's saved layout marks (`<w:lastRenderedPageBreak/>`) become newlines and never split a page. That last detail matters: a page break recorded by whoever last opened the file would otherwise make page numbers depend on their screen resolution or printer driver. Tabs become `\t`.
- **Text boxes:** Word stores a text box's content in its own paragraphs, nested inside the drawing that holds it. A naive parser drops the outer paragraph when it meets the inner one, so "before [box] after" turns into "before". Ogcode tracks paragraph depth and appends the inner text where it sits, so the sentence survives in reading order.
- **Tables:** a table has rows, and rows have cells. Each row becomes one line, with cells separated by tabs, so columns still line up. Several paragraphs inside one cell are joined with a space rather than a newline, and a nested table folds into the cell that holds it.
- **Footnotes and endnotes:** references are rendered as `[^1]` and `[^e1]`, numbered by their order of appearance rather than Word's internal ids, which skip after edits. The bodies come from the notes parts and are appended to the page that references them, so a note travels with the sentence it qualifies.
- **Tracked changes:** the extractor reads ordinary text runs. Text Word marked as deleted sits in a different element (`w:delText`) and never enters the text; insertions are ordinary runs and do enter. The result is the document with all revisions accepted, which is the only version a citation can be checked against.
- **Unusual and hostile files:** every part is inflated under a hard ceiling, so a few kilobytes of XML cannot expand into gigabytes and take the process down. Parts saved as UTF-16, or in another declared character set, are converted before parsing. A file that is not a valid archive returns an error instead of a crash.

> The extractor's job is not to render the page. It is to recover the text and the structure a reader would have seen.

## Inventing the Pages

The central design problem with Word documents is that they are a continuous reflowable stream. Word calculates page breaks dynamically, based on page margins, zoom, paragraph kerning, installed fonts and the typography engine of the operating system. A headless agent running in a container has none of that layout machinery.

If you don't paginate, you must feed the entire document to the model at once. In a 120-page technical specification, that eats about 80,000 tokens, drowns the prompt, degrades reasoning and prevents the agent from citing a verifiable source.

The naive alternative, cutting the text at a fixed word count, produces a subtle disaster:

```
[Page 12: word limit reached]
... We evaluated three potential storage architectures for the consensus
log: Raft over BoltDB, Paxos over RocksDB, and SQLite in WAL mode.

[Page 13: word limit reached]
1. Raft over BoltDB
The primary advantage of BoltDB is its pure-Go implementation and B+tree
crash resilience. However, write amplification under sustained throughput ...
```

A rigid ceiling regularly severs a heading from the paragraphs it describes, or splits a single paragraph across two arbitrary pages. If an indexer labels Page 12, it might see that storage options were evaluated but miss the actual analysis. If Page 13 starts with "1. Raft over BoltDB", the context identifying it as part of the consensus log decision was left behind on Page 12.

### Heading-Aware Pagination

Ogcode's paginator pairs a baseline word budget with **heading lookahead**:

```
                    word budget reached
                            │
                            ▼
Paragraphs: [█████████████████████] ─── flush point
                            │
                            │ ◄── lookahead window
Paragraphs: [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
                                ▲
                                │
                         Heading detected!
                                │
                   Flush HERE ──┘
              (Heading starts Page N+1)
```

1. **Explicit breaks win:** an explicit page break or an active `<w:pageBreakBefore/>` property flushes the page immediately, with a guard against manufacturing an empty first page.
2. **Baseline accumulation:** paragraphs accumulate until the page reaches its word budget.
3. **Heading lookahead:** once the budget is reached, the paginator keeps reading a little further, up to half the budget again. If a heading appears in that window, the page ends just before it. The heading opens the next page at the top, with its section intact.
4. **Ceiling fallback:** if no heading fits inside the lookahead, the page ends at the paragraph boundary where the budget was reached.

Soft breaks and Word's saved layout marks never take part in this. Only a real page break or a page-break-before property does.

The result is deterministic. The same file produces the same pages on every machine, with no fonts, margins or printer drivers involved. The trade is that page numbers belong to a version of the file: edit the document, re-index it, and a section can move. That is why the index checks the file before it trusts its own labels.

Word counts also have to work in languages that do not put spaces between words. A whitespace count would read a long Chinese memo as a single word and collapse the whole document into one page. Ogcode counts a long unspaced run by its length instead, so Chinese, Japanese and Thai prose paginate at roughly the same density as English.

<figure class="bp-chart">
  <div class="bp-chart-pan" tabindex="0">
    <img src="/blog/media/docx-pseudo-pages.svg?v=2" alt="Words per pseudo-page: a 7-page .docx, split by two rules. An explicit page break wins; otherwise a page ends near its word budget, or just before the next heading." width="960" height="348" loading="lazy">
  </div>
  <figcaption>Words per pseudo-page across a 7-page document. Explicit breaks end pages early; continuous text accumulates to the word budget, then defers the cut when a heading is close.</figcaption>
</figure>

### Resolving Styles and Outlines

Detecting a heading in Word is not a matter of looking for bold or large text. Heading definitions live in `word/styles.xml`:

```xml
<w:style w:type="paragraph" w:styleId="Heading1">
  <w:name w:val="heading 1"/>
  <w:pPr>
    <w:outlineLvl w:val="0"/>
  </w:pPr>
</w:style>
```

Ogcode's style resolver handles three layers of heading determination:

1. **Normalized heading names:** in a Spanish or German installation, Word displays localized style names and can generate localized style ids, but it still records the invariant English name in `<w:name w:val="heading 1"/>`. Reading that name identifies a standard heading regardless of the document's locale.
2. **Explicit outline levels:** a paragraph or style can declare an outline level from 0 to 8. Level 0 is the top title, levels 1 to 8 are subheadings, and level 9 is body text, which is ignored. Only paragraph styles count. A character style or a table style with a promising name is not a heading.
3. **Style inheritance chains:** custom organization styles often inherit from a base heading style. Ogcode walks the `basedOn` chain, with cycle detection, so a malformed document cannot loop forever and a house style called `ExecutiveHeading` still paginates like `Heading1`.

When a document is converted from plain text without heading styles, such as text run through Apple's `textutil`, the lookahead simply steps aside and the paginator relies on explicit breaks and the word budget.

## Two Tools, One Flow

Word document interaction in Ogcode mirrors the PDF model: two complementary tools exposed to the agent.

```
                  ┌──────────────────────┐
                  │      User asks       │
                  │  "What are our SLA   │
                  │     commitments?"    │
                  └──────────┬───────────┘
                             │
                             ▼
                  ┌──────────────────────┐
                  │      docx_index      │
                  │  (Reads the index,   │
                  │   never the archive) │
                  └──────────┬───────────┘
                             │
            Page 4: "SLA, uptime, maintenance windows"
                             │
                             ▼
                  ┌──────────────────────┐
                  │    read_docx_page    │
                  │  (Extracts page 4,   │
                  │   then keeps it in   │
                  │   a small cache)     │
                  └──────────┬───────────┘
                             │
                             ▼
                  ┌──────────────────────┐
                  │    Precise answer    │
                  │  with page citation  │
                  └──────────────────────┘
```

### 1. `docx_index`

When an agent needs to survey a document, calling `read_docx_page` on every page would flood the context window. Instead, it calls `docx_index`:

```json
{
  "path": "contracts/vendor-sla-2026.docx",
  "start_page": 1,
  "end_page": 10
}
```

The tool reads the local index and returns a dense catalogue of page summaries and topical labels. The ZIP archive is not touched, so the answer comes back in a few milliseconds.

Crucially, `docx_index` supports **range slicing**. In a 140-page technical specification, returning all 140 page labels at once would cost about 4,000 tokens. Range slicing lets the agent scan chapter by chapter, narrow to the relevant section, and only then fetch a page of text. Bounds are clamped to the document, and an empty range says which pages were asked for and what the document actually holds.

An index is only useful while it still describes the file. Before answering, `docx_index` compares the document's modification time with the time recorded when it was indexed. If the file is newer, the listing is labelled `(stale index)` and the agent is told to run `ogcode index` again. A file that has moved or disappeared says the index may be stale. A file that has never been indexed says to run the index first. None of these cases quietly return yesterday's page numbers.

### 2. `read_docx_page`

Once the agent identifies the target page, it calls `read_docx_page`:

```json
{
  "path": "contracts/vendor-sla-2026.docx",
  "page": 4
}
```

The agent receives only that page, with formatted tables, inline footnote markers and appended note bodies.

A second argument, `end_page`, reads a run of consecutive pages in one call. A section that spans pages 4 to 6 costs one archive read and one response instead of three. Each page in the run is introduced by a line such as `--- page 5 ---`, so the model never loses track of which text belongs where.

Bad bounds get a plain answer. A page past the end reports how many pages the document has and points back at `docx_index`. An empty document says it has no extractable text. A page with nothing on it says so instead of returning a blank the model has to interpret. A legacy `.doc` file gets the conversion hint, not a ZIP error.

### The In-Memory Cache

If an agent needs Page 4, then Page 5, then Page 6, unpacking and parsing a large archive on every call would be slow. The reader keeps a small LRU cache of extracted pages in memory:

- **Bounded footprint:** a fixed number of documents and a ceiling on the extracted text held. At least one entry is always kept, so a single oversized document cannot make the cache thrash.
- **Identity:** the absolute path, the file size, the nanosecond modification time, and a content stamp built from the first and last few kilobytes of the file.
- **Checked on every hit:** a `stat` and two short reads, then the comparison. No archive is parsed again.

The content stamp is the part that earns its keep. A file replaced by a different version, or restored from a backup that preserved the old timestamp, still fails the check. A cache that trusts a timestamp alone will eventually quote the wrong contract.

> A cache that trusts a timestamp is a cache that will eventually quote the wrong contract.

## The Token Math: Why On-Demand Pages Win

To see the difference this architecture makes, consider a real 137-page corporate document: 68,886 words and 502,276 characters of technical specifications, architecture diagrams and service definitions.

When an AI agent needs to answer questions about that document, there are three common approaches:

<figure class="bp-chart">
  <div class="bp-chart-pan" tabindex="0">
    <img src="/blog/media/docx-tokens-per-question.svg" alt="Tokens a 137-page .docx puts in front of the model: whole document dumped is approximately 125,600 tokens every turn; index prompt once is approximately 39,600 tokens for the whole file; one page on demand is approximately 840 tokens. Over ten questions, the dump carries about 1,255,600 tokens versus about 48,000 for the index plus ten pages." width="960" height="300" loading="lazy">
  </div>
  <figcaption>Tokens a 137-page Word document puts in front of the model across three access strategies. Over a ten-question exchange, the on-demand page model saves over 96% of the context window.</figcaption>
</figure>

| Dimension | Whole-Document Dump | RAG Pipeline (Vector Search) | Ogcode: Index, Then Pages |
|---|---|---|---|
| **Per-question prompt tokens** | about 125,600 tokens (the entire document, every turn) | about 2,500 to 5,000 tokens (5 to 10 arbitrary chunks) | **about 840 tokens** (one specific pseudo-page) |
| **Ten-question session** | **about 1,255,600 tokens** re-ingested | about 35,000 tokens plus 10 embedding round trips | **about 48,000 tokens** (catalogue plus 10 targeted pages) |
| **Token savings** | Baseline (0%) | about 97% (at the cost of broken context) | **over 96% reduction** (with complete context) |
| **Structural fidelity** | Preserved, but drowns prompt attention | Poor: splits tables, severs headings, drops footnotes | **High**: complete tables, resolved footnotes, text boxes in place |
| **Verifiability** | Difficult: the model must point into a 125k-token blob | Low: opaque chunk ids without page numbers | **Exact**: checkable page citations, such as "Page 14, Table 2" |
| **Latency and cost** | Severe: tens of seconds before the first token; large token bills | Medium: external embedding call and vector search | **Fast**: index lookup in milliseconds, pages served from memory |

### The Dump: The Context Cliff

Dumping the converted text directly into the agent's prompt puts about 125,600 tokens into the context window on every turn. In a conversation or a multi-step workflow, context compounds: by turn ten, the agent has ferried over 1.25 million tokens through the model.

Even with multi-million-token windows, context stuffing has well-known failure modes:

1. **The context cliff:** as we wrote in [the 64k context budget](/blog/context-budget-64k), recall and reasoning degrade well before the window is full. Critical clauses in the middle of a 137-page document get missed or misattributed.
2. **First-token latency:** processing a 125k-token prompt takes tens of seconds before the model generates a word.
3. **Compounding cost:** paying for 125,000 prompt tokens on every turn turns a simple research question into an expensive invoice.

### The RAG Shortcut: Severed Structure

The standard reaction to oversized documents is a retrieval pipeline: chunk the document into small token windows, embed the chunks, and retrieve the nearest ones.

For Word documents, that creates structural chaos:

- **Torn tables:** a 30-row pricing matrix gets sliced across chunks. The retrieved snippet might contain rows 12 to 18 without the header row, leaving the model to guess what each column means.
- **Dangling headings:** an outline heading lands at the bottom of one chunk while the obligation it defines starts at the top of the next.
- **Orphaned footnotes:** note bodies stored at the end of the file get embedded in isolation, severed from the clauses they qualify.
- **No page citations:** chunks have no idea where they sat in the document, so the agent cannot cite a page a human reviewer can open in Word.

### Ogcode: 96% Fewer Tokens, 100% Verifiable

Ogcode's two-tool approach treats the document like a database:

1. **Catalogue once (`docx_index`):** the agent scans page summaries and labels from the index. For the entire 137-page document, the catalogue is about 39,600 tokens. With range slicing, it can survey a chapter for a few hundred tokens per slice.
2. **Fetch on demand (`read_docx_page`):** the agent requests the page it needs, about 840 tokens, with its tables, footnotes and section context.

Across a ten-question interrogation, the agent consumes about 48,000 tokens in total (39.6k for the catalogue and 8.4k for ten page fetches). Against the whole-document dump at 1,255,600 tokens, **that removes more than 96% of token consumption**, keeps the model inside its peak-accuracy window, and gives every claim a page number a person can check.

## Transactional Indexing

Index generation happens in the background through `ogcode index`. Pages are labelled in batches, and a large document can take several.

Partial failure used to be the quiet kind of bug. Pages 1 to 100 land in the index, the next batch dies, and an agent asking for a page believes the document ends at page 100. A half-indexed contract is worse than no index. It answers with confidence, and then stops at the page where the data ran out.

That is no longer allowed to happen. Now:

1. A failed batch, an extraction error, or a document that yields no pages is logged, counted and returned as an error. Nothing is skipped in silence.
2. If any part of a document fails, every row for that document is deleted. The index never holds a partial document.
3. `ogcode index` prints the failure and exits non-zero, so whatever ran it can see that the index is incomplete.
4. The next run sees the document as unindexed and tries again from the beginning.

Progress is counted per document as well. A 250-page file that spans several batches finishes as one document, not as three partial ones. An agent either sees the whole document or knows the index failed.

> An index that is 80% finished is not 80% useful. It is wrong.

## What the Agent Sees

When an agent searches for specific clauses in a large Word document, the exchange is fast, bounded and verifiable:

```
> Check the SLA commitments in contracts/cloud-services.docx

[tool: docx_index path="contracts/cloud-services.docx" start_page=1 end_page=10]
  Page 1: Title, Executive Summary, Parties
  Page 2: Service Definitions, Infrastructure Scope
  Page 3: Data Protection, Security Certifications
  Page 4: Availability Guarantees, Service Credits, Scheduled Maintenance
  Page 5: Support Response Tiers, Escalation Matrix

[tool: read_docx_page path="contracts/cloud-services.docx" page=4]
  # 4. Service Availability Commitments
  The Provider guarantees 99.95% monthly uptime for all production endpoints[^1]...

  | Service Tier | Monthly Uptime | Service Credit |
  | Core API     | >= 99.95%      | 0%             |
  | Core API     | 99.0% - 99.94% | 10%            |
  | Core API     | < 99.0%        | 25%            |

  [^1]: Scheduled maintenance windows on Sundays 02:00-04:00 UTC excluded.
```

The model answers with the section and the page number:

> Under Section 4 of `contracts/cloud-services.docx` (Page 4), the monthly uptime commitment is 99.95%. If uptime falls between 99.0% and 99.94%, a 10% service credit applies; below 99.0%, the credit increases to 25%. Sunday maintenance windows between 02:00 and 04:00 UTC are excluded per footnote 1.

The answer is checkable. The prompt spent fewer than 800 tokens. No external process was spawned.

If the contract had been edited since the last index run, that first call would have carried `(stale index)` and a line telling the agent to run `ogcode index` again. The agent can still choose to use the old labels, but it cannot do the dangerous thing quietly: cite page 4 of a version it never read.

## Where This Is Going

The current implementation gives agents fast, deterministic access to the text, tables and notes that make up everyday office documents. Several areas remain open:

- **Dynamic list numbering (`word/numbering.xml`):** Word does not store literal numbers like `1.`, `2.1` or `(a)` in paragraph text. It stores abstract numbering definitions in a separate part. Reconstructing a deep legal list means evaluating those rules against the paragraph's level.
- **Hyperlink relationships (`word/_rels/document.xml.rels`):** an external URL is stored as a relationship id. Mapping ids to their targets would let an agent extract and follow links.
- **Headers and footers:** these live in separate parts and are currently excluded, so a copyright line or an author name does not repeat on every page. Text inside text boxes is included.
- **Tracked-changes diffs:** letting an agent ask for "revisions rejected" or inline diff markup when it is reviewing a legal redline, rather than always reading the accepted version.

Word documents do not need an office suite or a cloud service to be understood by an AI agent. Treat `.docx`, `.docm` and `.dotx` as the structured ZIP archives they are, stream their XML, respect their style hierarchies, and synthesize heading-aware pages. An agent can then read a contract, a memo or a specification with the same precision and speed it brings to code.
