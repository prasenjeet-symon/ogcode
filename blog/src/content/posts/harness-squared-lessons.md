---
title: "Harness squared: how an agent improves its own harness without an answer key"
description: "Model weights never change. In Harness², all improvement lives in files."
pubDate: 2026-11-08
tags: ["agents", "rsi", "harness", "evaluation"]
hero: "/blog/media/retail-three-harnesses.svg"
heroAlt: "Diagram comparing three ways to hand a 100,000-row retail spreadsheet to an agent: dumping the sheet as one 8.3-million-character CSV costs about 2,090,000 tokens per question, a RAG pipeline retrieves the top five of about 4,092 embedded windows at about 2,540 tokens, and an Ogcode sheet profile queried by SQL answers in 49 to 87 tokens."
---

The model is frozen. Everything learned during a session lives in files, not in weights.
