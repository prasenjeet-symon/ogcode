# Release Notes — v0.58.1

## Major: Web search cache and deep-search memory

Searches and page fetches are now served from a two-tier cache (in-memory LRU
over SQLite) with per-kind TTLs, so repeated reads and identical queries skip
the network entirely. Deep searches memoise their synthesised answer by query,
model, provider and UTC day, appending a served-from-cache note on a hit.

Conversations now carry their own deep-search history: every turn's deep
searches are recorded into the session memory file automatically at turn end,
deduplicated by query, with a prompt bullet telling the agent the loop owns
that section.

## Minor: Settings and storage

Search cache status and manual clear are exposed under the search settings page,
and the web-cache migration keeps the SQLite store consistent across restarts.
