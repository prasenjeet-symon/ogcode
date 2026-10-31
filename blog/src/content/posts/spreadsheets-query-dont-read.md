---
title: "How Ogcode reads a spreadsheet without reading it"
description: "An agent that tries to read a big spreadsheet loses before it starts. Ogcode gives every sheet a one-page profile and a real SQL question to ask, so a million-row workbook costs a few lines of context and comes back with an answer."
pubDate: 2026-10-31
tags: ["agents", "spreadsheets", "context-engine"]
hero: "/blog/media/spreadsheets-query-dont-read-hero.jpg"
heroAlt: "A dark isometric warehouse of stacked spreadsheet cards; a glowing orange agent orb holds one small clipboard card lit up in brand orange while a thin thread pulls a single answer slip out of the stacks."
ogImage: "https://ogcode.in/blog/media/spreadsheets-query-dont-read-hero.jpg"
---

Picture an accountant sitting in front of a ledger with a million rows. You
ask: how much did we ship to Norway in March? A good accountant does not read
the ledger. They look at the column headers, glance at a few rows, and run a
formula. The ledger stays closed. The answer comes back in seconds.

Now picture an AI agent handed the same job. For almost every coding
assistant, the plan is worse than useless: it tries to read the ledger.

This post is about how we fixed that in Ogcode. It turned out the fix is two
small things, and they share one brain: a one-page summary of every sheet, and
a tool that lets the agent ask the file a real question.

## First, how does an agent usually meet a spreadsheet?

A coding agent only sees text. Every photo of a spreadsheet you have ever
seen in a chat window is a screenshot; underneath, most assistants have two
moves, and neither is good:

1. **Convert it to text and print it.** A thousand-row sheet becomes a
   thousand-line blob in the conversation. A hundred-thousand-row sheet does
   not fit at all, so it gets cut, and the agent answers from whatever part
   survived the cut. It has no idea what fell off.
2. **Read it in pieces.** A read tool opens the file in slices: forty rows
   here, a few hundred there. To answer anything real, the agent has to read
   the whole thing, one expensive slice at a time, into a context window that
   then carries every slice around for the rest of the day.

And many assistants cannot open a real workbook at all. The `.xlsx` file you
dragged in first becomes a polite request to export it as CSV.

There is a deeper problem under both moves. A spreadsheet is not a document.
It has no plot, no conclusion, nothing to carry away after reading. What it
has is structure, and structure answers questions: count these, filter that,
group this, join that. Reading is a machine for taking in everything and
answering nothing. The question "how much did we ship to Norway in March?" is
eight numbers of work in a spreadsheet and eight hundred thousand tokens of
work by reading.

## Ogcode's answer: a profile page and one question

Ogcode gives the agent the accountant's starting move. It is two parts.

**Part one: every sheet gets a profile page.** Ogcode's project index
already handles documents by turning them into pages: a PDF becomes one page
per chapter, each carried by a short label the agent can search. Spreadsheets
ride the same rails, and the page text for a sheet is a profile: the sheet's
size, every column with its type and its range, the most common values, and
five sample rows. Here is the shape of one, for a sheet of an imaginary
orders workbook:

```
Sheet "Orders": 120,000 data rows, 8 columns
Columns:
- sku: text [TEXT]
- qty: integer [INTEGER], min 1, max 240
- ordered_at: date [TEXT], min 2024-01-02, max 2025-11-30
- region: text [TEXT], distinct: EU, US, APAC, LATAM
First 5 rows:
| ODE-4421 | desk-lamp-black | 12 | 2024-01-02 | EU |
```

That is one screen of text, and it is most of what an accountant needs
before running a formula: which column is money, which is dates, how many
rows, what kinds of values live in it. The workbook itself has not entered
the conversation. It never will.

The label system has a job here too. In the project map, a workbook does not
show up as a single mystery file. It shows its own table of contents:

```
book.xlsx — forecast scenario; budget assumptions; headcount plan
```

Three sheets, three labels, one entry. The agent finds the right sheet by
its label before touching anything.

**Part two: the question.** Next to the profile lives a query tool. The
agent writes ordinary SQL, and the tool runs it against the file directly.
Each sheet is a table. So the Norway question is:

```
SELECT SUM(qty) FROM Orders
WHERE region = 'EU' AND ordered_at BETWEEN '2025-03-01' AND '2025-03-31'
```

and the reply that lands in the conversation is a handful of lines, not a
ledger.

## The file never lands in the model

The plumbing is simple on purpose. When a query arrives, the tool builds a
small in-memory database from that one file: rows stream out of the
spreadsheet into that database, and stay there. The agent's SQL runs against
the database, and only the answer travels back into the conversation, capped
at 200 rows, on a three-second timer.

Two things fall out of that shape.

The agent can ask a hundred questions in a row and the conversation stays
flat. Schema first, a LIMIT sample, the aggregate, a cross-check: the
context cost of a hundred questions is the cost of a hundred answers, not
the cost of a hundred printouts of the file.

And the second: the profile and the query tool can never disagree with
each other, because they share one brain. The types on the profile page are
the very same types used to build the question tool's tables. If the profile
says `qty` is integers, then `qty` sorts and sums like integers in every
query the agent will ever run against it.

## The types refuse to flatter themselves

The type reader is deliberately pessimistic, and one choice in it is worth
showing.

In many tools, a column becomes "numeric" if most of its cells look like
numbers. Ours counts every cell as a vote, and a single "N/A" in any cell
flips the whole column to text. On purpose. A sum over a column that quietly
contains "N/A" is a confident wrong answer, and a confident wrong answer is
the worst thing an agent can hand you. If the column is not honestly
numbers, nothing downstream pretends it is.

Two more honest-reading details. The first thousand rows are read
completely, then the checker switches to a widening stride, so a column that
turns to text halfway down a huge file is still caught instead of being
silently mislabeled from a lucky sample. And Excel's private date format,
where a calendar day hides inside an ordinary-looking number, gets
translated into real dates, so `min 2024-01-02, max 2025-11-30` on the
profile page means what it says.

## Two sheets, one question

The capability jump is easiest to see when the answer lives in two sheets at
once. "What are the order quantities for the products that are out of
season?" is a lookup spread across the Orders sheet and the Products sheet.
By reading, that is a manual crosswalk: read one sheet, hold it, read the
other, match rows by hand, pray nothing was cut.

By query, it is one line of the thing spreadsheets were invented for:

```
SELECT o.sku, SUM(o.qty) AS sold
FROM Orders o JOIN Products p ON o.sku = p.sku
WHERE p.season = 'off'
GROUP BY o.sku ORDER BY sold DESC LIMIT 5
```

Before, that was the kind of task an agent started and abandoned, because
holding two workbooks in text was never a plan. Now the agent sees two
labeled sheets on one profile screen and writes one sentence of SQL.

## Three questions, answered from a real workbook

Everything above is argued, not shown. So we capped it the way you cap any
claim: pointed the same two tools, the profile and the query tool, at a real
workbook. A 23 MB `.xlsx` with 541,909 rows: a year and nine days of online
retail invoices, December 2010 to December 2011, 8 columns, 38 countries.
Not one of those rows entered a conversation. Here are three questions the
agent asked it, with the answers that came back. The queries are verbatim.

**How did revenue move over the year?** One group-by, thirteen rows back.

```sql
SELECT strftime('%Y-%m', InvoiceDate) AS month,
       ROUND(SUM(Quantity * UnitPrice)) AS revenue_gbp
FROM "Online Retail"
WHERE InvoiceNo NOT LIKE 'C%'
GROUP BY 1 ORDER BY 1
```

![Monthly revenue, net of cancellations. Revenue climbs through autumn and peaks at £1.5m in November 2011. December 2011 is shaded because the export stops on the 9th.](/blog/media/retail-monthly.svg)

Two things worth noticing, because an agent that only reads text could not
have done either. The `NOT LIKE 'C%'` is the agent refusing to book
cancellation invoices as revenue; without it, the peak months carry phantom
sales. And the flat-looking December at the right end is not a downturn:
that export stops on the 9th, and the honest answer says so instead of
"revenue collapsed". One chart, and both traps already dodged.

**Which products actually carry the store?** This one is a question in two
takes, because the first take lies. "Top products by revenue" asked naively
returns postage charges and administrative fees, which are stock codes like
any other and quietly outearn most of the catalogue. The agent's second
take restricts to real product codes, which all start with a digit:

```sql
SELECT Description,
       ROUND(SUM(Quantity * UnitPrice)) AS revenue_gbp
FROM "Online Retail"
WHERE InvoiceNo NOT LIKE 'C%' AND Quantity > 0
  AND StockCode GLOB '[0-9]*'
GROUP BY Description ORDER BY 2 DESC LIMIT 10
```

![Bar chart of the ten products with the highest revenue. Regency Cakestand 3 Tier leads at £174k, closely followed by Paper Craft, Little Birdie at £168k and the White Hanging Heart T-Light Holder at £106k.](/blog/media/retail-products.svg)

A £174k cakestand and a £168k craft kit. Ask by units instead of revenue and
the ranking changes shape; that comparison is the same question with a
different `ORDER BY`, not the same question against a second read of the
file. Every follow-up costs one small answer, which is the part of this
design we like most.

**When does the money arrive?** Hour of day, one more group-by:

```sql
SELECT strftime('%H', InvoiceDate) AS hour,
       ROUND(SUM(Quantity * UnitPrice)) AS revenue_gbp
FROM "Online Retail"
WHERE InvoiceNo NOT LIKE 'C%'
GROUP BY 1 ORDER BY 1
```

![Bar chart of revenue by hour of day. Business starts near 8am, runs hot between 9am and 4pm, peaks at noon around £1.5m and fades to nearly nothing after 6pm.](/blog/media/retail-hours.svg)

A clear office-hours rhythm: hot from 9 to 4, peak at noon, asleep by 7pm.
The kind of shape you only ask for once the first two questions cost
nothing, because it was never worth reading 541,909 rows to learn that.

The same file also answers geography in ten rows: the United Kingdom
accounts for the overwhelming share, with the Netherlands, EIRE, Germany and
France a long way behind as the working international markets. Three charts
of queries, one table of ten, and the whole workbook is still sitting on the
disk where we found it.

## Where this could still be better

No post about our own tools should pretend they are perfect. The limits,
in the open:

- **Very big sheets are capped at load time.** The question tool loads up
  to 100,000 rows per sheet, and it says so in every answer it gives. A
  count over a two-million-row sheet sees the loaded subset, honestly
  labeled, no pretended exactness.
- **Old formats are out.** The tool speaks `.xlsx`, `.xlsm`, `.csv` and
  `.tsv`. The ancient `.xls` binary and the OpenDocument `.ods` are not
  read today; save them as `.xlsx` or CSV and everything works.
- **Read-only, for now.** The agent can ask anything and cannot change
  anything. Writing back edits to a workbook is on the list, and we would
  rather do it carefully than quickly.
- **200 rows per answer.** Deliberate, so no single question can flood the
  conversation. A second question with `LIMIT` and `OFFSET` pages through
  the rest.

## The idea in one sentence

An agent facing a spreadsheet needs exactly what a good accountant needs:
the summary on the cover, and one honest question to run through the tool
built for it. Give the agent that, and a workbook that used to be a
conversation-ending blob becomes one profile screen and a handful of
answers, while the other hundred thousand rows sit quietly on the disk
where they belong.