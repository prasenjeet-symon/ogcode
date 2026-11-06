---
title: "Why Corporate .docx Files Break AI Agents"
description: "A .docx is a zip of XML with no pages in it. Ogcode reads Word documents (.docx, .docm, .dotx) directly without Word, LibreOffice, or external converters. It gives them heading-aware pages, extracts footnotes and tables, labels each page once, and opens one page at a time with checkable citations."
pubDate: 2026-11-06
tags: ["agents", "docx", "context-engine"]
hero: "/blog/media/docx-without-word-hero.jpg"
heroAlt: "A dark desk seen from above: a grid of blank frosted tiles glowing faintly orange, with one tile lifted clear of the grid by a small orange orb."
ogImage: "https://ogcode.in/blog/media/docx-without-word-hero.jpg"
---

Word documents are where work actually lives. The specification, the vendor agreement, the memo from legal, the architecture decision record, the onboarding guide nobody has edited since 2021. Ask an agent to read one, and one of three things usually happens: it shells out to LibreOffice or pandoc or `python-docx`, it sends the file to a cloud conversion service and waits, or it reads the filename and invents the contents.

All three treat the format as an opaque black box requiring a heavyweight translation engine. It isn't. An Open Packaging Convention Word document is a standard ZIP file full of XML, and the XML is mostly structured paragraphs.

We previously wrote about [reading PDFs one page at a time](/blog/pdfs-one-page-at-a-time). This is the exact same architectural pattern applied to the document format that dominates corporate workflows. The mechanics differ in one fundamental respect: a PDF has explicit physical pages pre-rendered by a layout engine. A Word document has no pages at all.

Most of what makes Word document handling interesting is where those pages come from, how headings prevent severed context, and how an agent navigates them without blowing its context window.

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

The Open Office XML (OOXML) standard wraps an entire document ecosystem into a single container. Ogcode's document indexer (`internal/docindex.DocxExtensions`) recognizes all three standard OOXML word-processing containers:

- **`.docx`**: Standard Word documents.
- **`.docm`**: Macro-enabled Word documents (the underlying XML structure is identical; the macro payload lives in a separate binary part).
- **`.dotx`**: Word document templates.

*(Legacy binary `.doc` files from Word 97–2003 are OLE2 Compound File Binary containers, not ZIP archives, and are excluded by design.)*

To read any of these, ogcode needs no Cgo bindings, no Python runtimes, and no external command-line utilities. Go's standard library `archive/zip` opens the archive in memory, and `encoding/xml` streams the tokens.

The only mandatory component is `word/document.xml`, which houses the document body. Auxiliary parts like `word/styles.xml`, `word/footnotes.xml`, and `word/endnotes.xml` are loaded when present. If an auxiliary part is missing or corrupted, extraction degrades gracefully rather than aborting.

## Streaming the XML

A typical production Word document can easily contain hundreds of thousands of XML tokens. Parsing the entire DOM into memory with tree-based structures wastes allocations and time. Instead, ogcode uses a streaming XML tokenizer (`xml.Decoder.Token()`) in `internal/docx/docx.go`:

```
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

The extractor handles the core structural elements of WordprocessingML:

- **Paragraphs and Runs:** `<w:p>` begins a paragraph block, `<w:r>` denotes a character run with shared formatting, and `<w:t>` carries literal character data.
- **Breaks and Tabs:** An explicit `<w:br w:type="page"/>` forces an immediate page boundary. Soft breaks (`<w:br/>`), carriage returns (`<w:cr/>`), and layout artifacts (`<w:lastRenderedPageBreak/>`) emit newlines without splitting pages. *(Ignoring `lastRenderedPageBreak` ensures extraction is deterministic regardless of which screen resolution or printer driver Word last used to save the file).* Tabs (`<w:tab>`) translate cleanly to `\t`.
- **Tables:** Tables (`<w:tbl>`) consist of rows (`<w:tr>`) and cells (`<w:tc>`). Rather than dumping table text into an undifferentiated stream of words, ogcode maintains row and cell stacks. Each table row is emitted on its own line, with cells separated by tab characters (`\t`) and multiple paragraphs within a single cell separated by `\n`. Nested tables are fully supported via hierarchical state tracking.
- **Footnotes and Endnotes:** References inside the body (`<w:footnoteReference w:id="3"/>` and `<w:endnoteReference w:id="1"/>`) are detected on the fly. Ogcode renders them as standard Markdown footnotes (`[^1]` and `[^e1]`), numbered sequentially by their order of appearance in the text rather than Word's internal XML IDs, which often contain gaps after editing. The note bodies are extracted from `word/footnotes.xml` and `word/endnotes.xml` (omitting internal separators) and appended directly to the referencing page.
- **Tracked Changes:** In revisions mode, deletions wrapped in `<w:del>` contain `<w:delText>`, while insertions wrapped in `<w:ins>` contain standard `<w:r><w:t>`. Ogcode skips deleted text tokens and processes insertions, reading the document in its "all revisions accepted" state.

## Inventing the Pages

The central design problem with Word documents is that they are a continuous reflowable stream. Word calculates page breaks dynamically on screen based on page margins, zoom levels, paragraph kerning, installed fonts, and operating system typography engines. A headless agent running in a Linux container or on a Darwin server has none of that layout machinery.

If you don't paginate, you must feed the entire document to the model at once. In a 120-page technical specification, that eats 80,000 tokens, drowns the prompt, degrades reasoning, and prevents the agent from citing verifiable sources.

The naive alternative, cutting the text strictly every 500 words, produces a subtle disaster:

```
[Page 12: 500 words]
... We evaluated three potential storage architectures for the consensus
log: Raft over BoltDB, Paxos over RocksDB, and SQLite in WAL mode.

[Page 13: 500 words]
1. Raft over BoltDB
The primary advantage of BoltDB is its pure-Go implementation and B+tree
crash resilience. However, write amplification under sustained throughput ...
```

A rigid word-count ceiling regularly severs section headings from the paragraphs they describe, or splits a single paragraph across two arbitrary pages. If an indexer labels Page 12, it might see that storage options were evaluated, but miss the actual analysis. If Page 13 starts with "1. Raft over BoltDB", the context identifying it as part of consensus log evaluation was left behind on Page 12.

### Heading-Aware Pagination

To solve this, ogcode's paginator (`internal/docx.groupPages`) pairs a baseline word budget with **heading lookahead**:

```
                         WordsPerPage (500 words)
                                  │
                                  ▼
Paragraphs: [█████████████████████] ─── flush threshold reached
                                  │
                                  │ ◄── Lookahead window (up to 750 words)
Paragraphs: [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
                                      ▲
                                      │
                               Heading detected!
                                      │
                         Flush HERE ──┘
                    (Heading starts Page N+1)
```

1. **Explicit Breaks Win:** If the document has an explicit page break (`<w:br w:type="page"/>`) or an active `<w:pageBreakBefore/>` property on a paragraph, the current page flushes immediately (with guards against emitting empty leading pages).
2. **Baseline Accumulation:** The paginator accumulates paragraphs until it reaches `WordsPerPage` (500 words).
3. **Heading Lookahead:** Once 500 words are accumulated, the paginator does not immediately cut the page. Instead, it peers ahead up to `pageLookaheadWords` (750 words total accumulated text). If a heading appears within that window, the flush is deferred until right before that heading. The current page ends cleanly, and the heading begins the next page at the top.
4. **Ceiling Fallback:** If no heading is found within the 750-word lookahead budget, the page flushes at the paragraph boundary nearest the 500-word mark.

### Resolving Styles and Outlines

Detecting a heading in Word isn't just checking whether text is bold or large. Heading definitions live in `word/styles.xml`:

```xml
<w:style w:type="paragraph" w:styleId="Heading1">
  <w:name w:val="heading 1"/>
  <w:pPr>
    <w:outlineLvl w:val="0"/>
  </w:pPr>
</w:style>
```

Ogcode's style resolver (`internal/docx/styles.go`) handles three layers of heading determination:

1. **Normalized Heading Names:** In non-English installations of Word (such as Spanish or German), the UI displays localized style names like `Título 1` or `Überschrift 1`, and generates localized style IDs (`styleId="Ttulo1"`). However, Word consistently records the standard invariant English name in `<w:name w:val="heading 1"/>`. Ogcode inspects `w:name` to identify standard heading hierarchies regardless of document locale.
2. **Explicit Outline Levels:** A paragraph or style can declare an explicit outline level (`<w:outlineLvl w:val="0"/>` to `8`). Level 0 represents the top title; levels 1–8 represent subheadings. Level 9 is defined by the standard as body text and is ignored.
3. **Style Inheritance Chains:** Custom organization styles (e.g., `ExecutiveHeading`) often inherit from base heading styles via `<w:basedOn w:val="Heading1"/>`. Ogcode resolves the `basedOn` inheritance tree iteratively, complete with cycle detection to prevent infinite loops on malformed documents.

When documents are converted from raw plain text without heading styles (such as text converted using Apple's `textutil`), the heading lookahead gracefully steps aside, and the paginator relies on explicit breaks and the 500-word paragraph-boundary ceiling.

## Two Tools, One Flow

Word document interaction in ogcode mirrors the PDF interaction model: two complementary tools exposed to the agent.

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
                  │  (Reads SQLite store │
                  │  no ZIP unpack cost) │
                  └──────────┬───────────┘
                             │
            Page 4: "SLA, uptime, maintenance windows"
                             │
                             ▼
                  ┌──────────────────────┐
                  │    read_docx_page    │
                  │   (Extracts page 4   │
                  │    via LRU cache)    │
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

The tool queries the local SQLite `doc_page_index` table and returns a dense catalogue of page summaries and topical labels. The entire operation takes less than 2 milliseconds because the ZIP archive isn't touched.

Crucially, `docx_index` supports **range slicing** (`start_page` and `end_page`). In a 140-page technical specification, returning all 140 page labels at once would cost 4,000 tokens. Range slicing lets the agent scan chapter by chapter, zeroing in on the relevant section before fetching a single page of text.

### 2. `read_docx_page`

Once the agent identifies the target page from the index, it calls `read_docx_page`:

```json
{
  "path": "contracts/vendor-sla-2026.docx",
  "page_num": 4
}
```

The agent receives only that specific page, complete with formatted tables, inline footnote markers, and appended footnote bodies.

### The In-Memory LRU Cache

If an agent needs Page 4, then Page 5, then Page 6, unpacking and re-parsing a 50 MB `.docx` archive on every call would introduce unbearable latency.

`internal/tool/read_docx_page.go` maintains an in-memory Least-Recently-Used (LRU) cache (`docxPages`):

- **Bounded Footprint:** The cache holds up to 8 documents (`docxPageCacheCap = 8`) or 32 MiB of extracted text (`docxPageCacheMaxBytes = 32 << 20`).
- **File Validation:** Cache entries are keyed by the file's absolute path and validated on every read against `os.Stat` (checking file size and nanosecond modification timestamps). If someone saves a new version of the document in Word, the cache invalidates instantly.
- **Cache Eviction Guarantee:** The cache eviction logic guarantees that at least one document entry is always preserved, preventing thrashing even if a single oversized document exceeds the memory target.

For consecutive page reads, the archive unpack cost is paid exactly once. Subsequent reads take less than a microsecond, just the time required for a single `stat` system call.

## Transactional Indexing

Index generation happens in the background via `ogcode index`. For massive documents, pages are indexed in batches of 100 (`batchSize = 100`).

In distributed or concurrent environments, partial failures can corrupt document indices: if an indexer finishes pages 1–100, and a network failure or process termination kills batch 2 (pages 101–150), an agent querying `docx_index` would assume the document only has 100 pages.

Ogcode enforces strict transactional integrity (`internal/indexer/indexer.go`):

```go
missing := missingPageNums(pages, existingPages)
if len(missing) > 0 {
    // Incomplete index state detected
    store.DeleteByDoc(ctx, docPath)
}
```

`missingPageNums` compares the set of successfully extracted pages with the records in SQLite. If any pages are missing, or if an indexing batch fails:
1. Incomplete records for that document are swept and purged (`DeleteByDoc`).
2. `ogcode index` reports the failure and exits with a non-zero exit code.
3. Subsequent indexing runs detect the missing document and re-index it from scratch.

An agent never sees a half-indexed document.

## What the Agent Sees

When an agent searches for specific clauses in a large Word document, the exchange is fast, bounded, and verifiable:

```
> Check the SLA commitments in contracts/cloud-services.docx

[tool: docx_index path="contracts/cloud-services.docx" start_page=1 end_page=10]
  Page 1: Title, Executive Summary, Parties
  Page 2: Service Definitions, Infrastructure Scope
  Page 3: Data Protection, Security Certifications
  Page 4: Availability Guarantees, Service Credits, Scheduled Maintenance
  Page 5: Support Response Tiers, Escalation Matrix

[tool: read_docx_page path="contracts/cloud-services.docx" page_num=4]
  # 4. Service Availability Commitments
  The Provider guarantees 99.95% monthly uptime for all production endpoints[^1]...

  | Service Tier | Monthly Uptime | Service Credit |
  | Core API     | >= 99.95%      | 0%             |
  | Core API     | 99.0% - 99.94% | 10%            |
  | Core API     | < 99.0%        | 25%            |

  [^1]: Scheduled maintenance windows on Sundays 02:00-04:00 UTC excluded.
```

The model generates an answer anchored directly to Page 4:

> Under Section 4 of `contracts/cloud-services.docx` (Page 4), the monthly uptime commitment is 99.95%. If uptime falls between 99.0% and 99.94%, a 10% service credit applies; below 99.0%, the credit increases to 25%. Sunday maintenance windows between 02:00 and 04:00 UTC are excluded per footnote 1.

The answer is checkable. The prompt spent fewer than 800 tokens. No external processes were spawned.

## Where This Is Going

The current implementation gives agents fast, deterministic access to the text, tables, and notes that make up everyday office documents. Several areas remain open for future enhancement:

- **Dynamic List Numbering (`word/numbering.xml`):** Word does not store literal numbers (`1.`, `2.1`, `(a)`) in paragraph text; it stores abstract numbering definitions in a separate XML part. Reconstructing deep multilevel legal lists requires evaluating these numbering rules across runs.
- **Hyperlink Relationships (`word/_rels/document.xml.rels`):** External web URLs in Word documents are stored as relationship IDs (`r:id="rId4"`). Mapping those relationship IDs to their target URIs will allow agents to extract and follow embedded URLs.
- **Headers and Footers:** Document headers and footers live in separate XML parts (`word/header1.xml`, `word/footer1.xml`) and are currently excluded to prevent repeating copyright notices and author names from cluttering every page.
- **Tracked Changes Diffs:** Allowing the agent to request "revisions rejected" or inline diff markup (`[+inserted text+]`, `[-deleted text-]`) when reviewing legal redlines.

Word documents do not require an office suite or an external service to be understood by an AI agent. By treating `.docx`, `.docm`, and `.dotx` as the structured ZIP archives they are, streaming their XML, respecting their style hierarchies, and synthesizing intelligent heading-aware pages, agents can read contracts, memos, and specs with the same precision and speed they bring to code.
