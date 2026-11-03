---
title: "The context window has a second number, and it is the one that matters"
description: "Models advertise a huge context window. The second number, how much of it a model can actually use well, is far smaller, and it decides whether your agent works."
pubDate: 2026-11-03
updatedDate: 2026-11-03
tags: ["agents", "context-window", "context-engine", "accuracy", "speed", "compaction", "research"]
hero: "/blog/media/context-budget-64k-hero.jpg"
heroAlt: "A dark isometric scene: a tall chaotic stack of grey paper sheets dissolving into shadow on the left, a glowing orange slot cut through a dark slab in the middle, and a small neat stack of three orange cards on the right, joined by a thin thread of orange light."
ogImage: "https://ogcode.in/blog/media/context-budget-64k-hero.jpg"
---

Your agent has been working for forty minutes.

Its context window says it can hold 200,000 tokens. Right now it is carrying 180,000 of them: a config file it read once and never needed, the same file read properly the second time, six thousand lines of test output, its own notes to itself, and somewhere near the top, the one paragraph where you explained what you actually wanted.

Every step sends all of that back. The model has to read all of it before it can answer, and every step charges you for all of it. The sentence that matters most is also the oldest thing in the pile, which makes it the thing the model is least likely to notice.

That is the part most people miss. There are two numbers in play here, and only one of them is printed on the box. The first is how much the model will accept before it breaks. The second is how much of that it can actually use well, and the second one is much smaller. It is also the one that decides whether your agent does good work.

**In short:** a bigger window lets you hand a model more stuff. It does not make the model better at using it, and it does not make it faster either. Ogcode is built the other way round: keep the pile small, keep the question next to the evidence, and write down anything that has to survive. A lot of its accuracy, and a lot of its speed, comes from that one habit.

## What is a context window?

A model cannot reread your conversation the way you can. Every step, it gets one block of text, and that block is everything relevant so far: your messages, its answers, the files it opened, the commands it ran. That block is the context window.

The size is measured in tokens. A token is a small chunk of text, roughly three quarters of a word. So a 200,000 token window is around three hundred pages of ordinary text, handed over at once, again and again, on every single step.

Here is the catch. The window is a maximum, not a guarantee. It tells you the model will not fall over with that much text in front of it. It tells you nothing about whether the model will still be any good at that size.

Plenty of published research says it will not.

## Models get worse long before the window is full

Four different kinds of study keep landing on the same answer, and none of them went looking for it.

**Where the answer sits.** One team gave a model twenty documents and a question. Exactly one document held the answer, and they moved that one document around the pile. On top of the pile: 75.8% correct. In the middle: 53.8%. Handing the model no documents at all: 56.1%.

Read that again. The middle of the pile scored worse than having nothing to read. Same model, same question, same facts. The only thing that moved was position.

<figure>
  <img src="/blog/media/ctx-position.svg" alt="Bar chart: how often a model answered correctly when the one useful document sat at the start of a twenty document stack, at the end, or in the middle. First scores 75.8 percent, last 63.2 percent, middle 53.8 percent, and handing over no documents at all scores 56.1 percent" width="960" height="372" loading="lazy">
  <figcaption>Same model, same question, one document moved. Getting the evidence into the middle of the pile scored worse than handing over nothing to read.</figcaption>
</figure>

**How much it has to read.** Another team kept the question exactly the same and only made the input longer, filling it with text that had nothing to do with the answer. Accuracy still fell, from 0.92 to 0.68 on average across the models they tested, at around three thousand tokens. Not three hundred thousand. Three thousand. Just making the pile bigger was enough to make the model worse.

**How big the window really is.** RULER tested seventeen models that all claimed at least 32,000 tokens. Only about half of them held up at 32,000. NoLiMa then removed the trick that makes long context tests easy, the fact that the answer shares exact words with the question. Of thirteen models claiming 128,000 tokens or more, eleven dropped below half of their own short-context score by 32,000.

The most uncomfortable result comes from a team that gave a model perfect retrieval. They proved the evidence sat in the prompt, and the answers were still wrong. When the passage is definitely there and the model still misses it, the problem is not search. It is attention.

**Where the slide starts.** Databricks ran more than two thousand evaluations across thirteen models and found the curve rises before it falls. Llama-3.1-405B peaked and started slipping after about 32,000 tokens. GPT-4-0125-preview peaked around 64,000. Chroma tested eighteen models, changing nothing but the length, and found quality slipping unevenly the whole way up, with no safe size anywhere.

<figure>
  <img src="/blog/media/ctx-cliff.svg" alt="Chart: where six long context studies find quality starts to slide, on a log scale from one thousand to one million tokens. Levy at three thousand, Du at thirty thousand, RULER at thirty two thousand, NoLiMa at thirty two thousand, Llama 3.1 405B at thirty two thousand, GPT 4 0125 at sixty four thousand. A shaded band runs from thirty two to sixty four thousand, where four of the six land. Two brackets under the axis mark one Ogcode step and one advertised window" width="960" height="470" loading="lazy">
  <figcaption>Six independent studies on one log scale. The shaded band is 32,000 to 64,000 tokens, where four of the six land. The two brackets underneath compare the size of one Ogcode step with the size of one advertised window.</figcaption>
</figure>

Look at where those turning points land: 32,000 to 64,000 tokens. Not 180,000. Not the last inch of the window.

> The cliff is not at the edge of the window. It is at a small fraction of what the model was sold as.

## What a full window costs you

Four costs arrive together. Only one of them shows up on your bill.

**Money.** The whole conversation is re-sent on every step. A request carrying 180,000 tokens gets charged for 180,000 tokens on every tool call, every retry, every small correction. You are not paying once to read a file. You are paying for it on every step until the turn ends. This is the cost everyone notices, and it is the least interesting one.

**Speed.** Reading takes time. A model cannot begin its answer until it has taken in the request, so the wait before the first word grows with the amount of text sitting in front of the question.

That part is not a research claim, so we measured it. One question, one model, and only one thing changing: how much text came before the question, from about two thousand tokens up to almost a million.

<figure>
  <img src="/blog/media/ctx-speed.svg" alt="Two charts over request size, from one thousand to a million tokens. Above, seconds to the first token climbs as the input grows, from eight tenths of a second at two thousand tokens to seventy five seconds at nine hundred and seventy four thousand. Two shaded bands mark five to twenty thousand tokens, where a single step sits, and one hundred and twenty eight thousand to a million, where a request carrying the whole window sits. Below, tokens written per second stays broadly flat, between about two hundred and three hundred, until the largest request" width="960" height="560" loading="lazy">
  <figcaption>Measured in November 2026. One model, one question, twelve input sizes from two thousand tokens to nine hundred and seventy four thousand. Every size was sent as a fresh prompt so nothing was served from cache, streamed, with a short answer, and the median of three runs is shown. The bands are the comparison that matters: every step Ogcode sends lands in the left one, and a request that hands the model its whole window lands in the right one.</figcaption>
</figure>

The wait climbs with the size of the request, and it never comes back down. At two thousand tokens it is under a second. At just under a million it is seventy five. The only thing that changed is how much text the agent chose to send.

Look at the second chart too. Once the model starts writing, it writes at close to the same rate almost the whole way up. The extra time was never typing. It was reading.

So the size of the request is not just what the model is charged for. It is what the model pays in time before it can say anything at all. And it is the same decision that decides whether the answer is any good: a small request is cheap, quick, and easy to attend to. Money, time and accuracy all trace back to how much text the request carries.

Ogcode makes that decision. It cannot make a model faster and does not try. What it does is keep every request at the size of a step instead of the size of a session, which puts every request in the left band. The model is the same. The question is the same. The wait is shorter, because there is less to read first.

> Every token you leave in the window is a token the model has to read past before it answers.

**Attention.** A model spreads its attention across whatever you give it. Everything in the request competes with the question, including the two hundred lines of stack trace that turned out to be a red herring. You cannot add material to a prompt without changing what the model pays attention to. Researchers have a name for the effect: context rot. It behaves like a slope rather than a cliff, and it starts earlier than anyone would like. One mechanistic study traced it to attention drifting off the question as the input grows, and Anthropic's guidance to the people building agents says the same thing: treat the window as a budget, not a container.

**Position.** This is the one people forget. A fact at the top of a long request is not worth what the same fact is worth sitting next to the question. Making the request smaller helps, but only if the shrinking keeps the question and the evidence close together.

There is a tempting shortcut here: just cut tokens. LongLLMLingua got a 21.4% gain while using about four times fewer tokens, but only because its compression knew what the question was. Compress blind and the results get worse.

> Cutting tokens is not the fix. Cutting the right tokens is.

## What Ogcode does differently

Ogcode is an open source coding agent. It is not a model, it is the thing that decides what the model gets to see.

Six decisions matter, and all six are about keeping the request small and keeping it close to the question. That is an accuracy decision. It is also a speed decision.

### 1. Every turn starts on a clean page

Ogcode does not hand the model a transcript of the whole session. It sends three things: what you just asked, the final answer from the turn before, so the thread stays connected, and a rolling summary of everything older. The rest stays on disk, where the agent can reach it by name.

The effect is simple. An hour into a session, the model is not wading through ten finished tasks to find your question. Your question is at the top, because it is the first thing in the list.

This is also why the emergency brake rarely comes on in normal use. That brake is the summary step in section 4. When a request gets big, it is because one turn is doing a lot of reading, not because forty turns piled up.

### 2. One tool result cannot take over the turn

A tool's output is capped before it enters the conversation, and the useful beginning is what survives. A runaway test log or a minified bundle cannot decide how the rest of the turn goes, or what the rest of the turn costs.

The cap is a guard rail, not a gag. When the agent asks for one specific file on purpose, silently handing it half a file is worse than the size, because it would reason from half the truth and never know. The limit is there to catch accidents, not to overrule a deliberate read.

### 3. The agent is told when it has read too much

A turn that reads a lot is usually a turn that has lost the thread. Once a big share of the request has come in as file contents, search results and fetched pages, the next result carries a note: here is how much has arrived, all of it stays in context, and all of it is re-sent on every step from here, so summarize now.

The note also says what the summary has to keep. File paths, line ranges, conclusions, decisions, exact values, and the answers you gave when the agent asked you something. That list is the difference between a summary the next step can use and one that loses the plot.

Reading a lot and running long are two different problems, so both are watched. The reminder keeps arriving until the context actually shrinks. A warning that fires once is a notification. One that repeats until it is acted on works like a budget.

> Reading more and knowing more stopped being the same thing a long time ago.

### 4. The summary happens before the window is full

Most turns never touch this step. Because every turn starts on a clean page and every tool result is capped, a request only gets big when a single turn is genuinely reading a lot at once. So compression is rare by design. It is the last line of defence, not the normal path.

When it does fire, the agent compresses instead of failing. The point where it does that is worked out from the model's own window. A small model and a large one cannot share one number, and some room is always kept free for the reply.

Two details matter more than the numbers. The agent compares its own estimate against the exact count that comes back on every response, because that count is right and measuring early is cheaper than overflowing. And if a request is rejected as too long anyway, that rejection is remembered as the model's real window, so the next turn is sized against the length the model actually accepted, not the length it advertised.

The summary is not a chop. The most recent messages stay word for word, the older part gets summarized, and each new summary folds in the previous one. What the first summary caught is still there in the third.

After a compression the agent carries on from the summary plus the recent tail. Without that step it would rebuild its history, find itself too big again, and compress once per step for the rest of the run.

> A brake you can feel working is a brake that arrived too late.

That one step is worth a post of its own, and it is the next one.

### 5. What matters is written down, not remembered

Compression loses things. No clever prompt makes it lossless, and no summary is a transcript. The answer is not a better summary. The answer is that the important facts are already on disk before anything can lose them.

So a turn writes down what it learned: what the project knows, what this session has already worked out, and which searches it already paid for. When the agent needs one of those back, it reads the file. There is more on that in [how an agent remembers a project](/blog/memory-is-table-stakes/).

The prompt the agent starts with carries one line pointing at each file, never the contents. A pointer costs a sentence. The file costs the window.

> A summary you have to trust is worth less than a file you can open.

### 6. Files get asked questions, not swallowed whole

The cheapest token is the one that never enters the conversation.

Ogcode indexes PDFs, Word documents and spreadsheets into short page labels, and gives the agent a SQL tool for workbooks. A question about a half-million-row sheet gets answered by a query, not by loading every row into the chat. It is the same idea at a different size: fewer tokens in, better answer out, because the model is never asked to hold the whole file in its head while it thinks.

Two other posts go deeper on that side: [how a PDF becomes pages you can ask about](/blog/pdfs-one-page-at-a-time/), and [how a spreadsheet gets queried instead of read](/blog/spreadsheets-query-dont-read/).

## What this does not fix

No post about its own product should pretend the product is perfect.

**Position still matters.** A smaller request helps, but it does not reorder what is in it. Evidence you paste at the top of a long message is still evidence at the top. Ogcode nudges the agent to work near the question. No setting fixes that for you.

**Compression is lossy even when it folds forward.** Anything that only ever existed inside a summary is a summary now. That is exactly why the facts worth keeping live in files and not in the conversation.

**No single number is right for every model.** Which is why the point of no return is worked out per model, and why it adjusts when a model turns out to accept less than its published window claimed.

**Architecture cannot make a weak model strong.** What it can do is stop a good model from being handed a bad request. That turns out to be most of the difference between a session that lands and one that wanders.

## The whole idea in one sentence

An agent's accuracy on the next step is decided by what you put in front of the model on that step. So is the wait before the answer starts. Keep the request small. Keep it close to the question. And keep whatever has to survive outside of it, in a file, where no summary can erase it.

> A bigger window is not a bigger brain.

## References

Every number in this post comes from one of the papers and reports below, except the speed chart, which we measured ourselves.

1. Nelson F. Liu, Kevin Lin, John Hewitt, Ashwin Paranjape, Michele Bevilacqua, Fabio Petroni, Percy Liang, Tatsunori Hashimoto, Shehzaad Dhuliawala, Taylor Sorensen. "Lost in the Middle: How Language Models Use Long Contexts." TACL, 2024. [arxiv.org/abs/2307.03172](https://arxiv.org/abs/2307.03172)
2. Mosh Levy, Alon Jacoby, Yoav Goldberg. "Same Task, More Tokens: the Impact of Input Length on the Reasoning Performance of Large Language Models." ACL, 2024. [aclanthology.org/2024.acl-long.818](https://aclanthology.org/2024.acl-long.818/)
3. Cheng-Ping Hsieh, Simeng Sun, Samuel Kriman, Shantanu Acharya, Dima Rekesh, Fei Jia, Boris Ginsburg. "RULER: What's the Real Context Size of Your Long-Context Language Models?" COLM, 2024. [arxiv.org/abs/2404.06654](https://arxiv.org/abs/2404.06654)
4. Ali Modarressi, Hanieh Deilamsalehy, Franck Dernoncourt, Trung Bui, Ryan A. Rossi, Seunghyun Yoon, Hinrich Schütze. "NoLiMa: Long-Context Evaluation Beyond Literal Matching." ICML, 2025. [arxiv.org/abs/2502.05167](https://arxiv.org/abs/2502.05167)
5. Yufeng Du, Minyang Tian, Srikanth Ronanki, Subendhu Rongali, Sravan Babu Bodapati, Aram Galstyan, Azton Wells, Roy Schwartz, Eliu A. Huerta, Hao Peng. "Context Length Alone Hurts LLM Performance Despite Perfect Retrieval." Findings of EMNLP, 2025. [aclanthology.org/2025.findings-emnlp.1264](https://aclanthology.org/2025.findings-emnlp.1264/)
6. Quinn Leng, Jacob Portes, Sam Havens, Matei Zaharia, Michael Carbin. "Long Context RAG Performance of LLMs." Databricks, 2024. [databricks.com/blog/long-context-rag-performance-llms](https://www.databricks.com/blog/long-context-rag-performance-llms)
7. Kelly Hong, Anton Troynikov, Jeff Huber. "Context Rot: How Increasing Input Tokens Impacts LLM Performance." Chroma Technical Report, 2025. [research.trychroma.com/context-rot](https://research.trychroma.com/context-rot)
8. TMLS. "Context Rot: A Mechanistic and Empirical Study of Long-Context Degradation." TMLS Research, 2025. [tmls.nyc/research/context-rot-mechanistic](https://tmls.nyc/research/context-rot-mechanistic)
9. Anthropic. "Effective Context Engineering for AI Agents." Anthropic Research, 2025. [anthropic.com/research/effective-context-engineering-for-ai-agents](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents)
10. Huiqiang Jiang, Qianhui Wu, Xufang Luo, Dongsheng Li, Chin-Yew Lin, Yuqing Yang, Lili Qiu. "LongLLMLingua: Accelerating and Enhancing LLMs in Long Context Scenarios via Prompt Compression." ACL, 2024. [arxiv.org/abs/2310.06839](https://arxiv.org/abs/2310.06839)
