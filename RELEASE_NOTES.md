# Release Notes — v0.58.3

## Patch: The subreddit link is gone from every public surface

r/ogcode is gone, so every link to it is removed rather than left pointing at a room
nobody is in. The home page loses it in the top navigation, the mobile menu, the
footer social row, the Community column and the legal row; the README's community
line drops it too, as do the blog and docs footers. The home page's click analytics
no longer carries a branch for a link nobody can click, and the mobile menu's one
remaining GitHub button now spans its row instead of half of it. In the app, the
About page in Settings lists Telegram and WhatsApp in a two-up row, with the
subreddit card and its glyph gone.

## Patch: The small screens get their targets back

On a phone the sites' inline text links were only as tall as their font, so the footer
rows, the legal line, the docs search field and the blog's comment fallback all asked
for a steadier finger than they gave. Each now carries its own padding, taken out of
the surrounding gap so the column rhythm is unchanged. The docs sidebar stops repeating
its heading and full navigation on a narrow screen, leaving the search box above the
disclosure that already lists every page, and the search field's clear button now sits
inside the field instead of trailing it as a stray label. The three measured charts in
the context window post keep the size they were drawn at and pan sideways rather than
scaling their labels down to four pixels, and the home page's narrow-screen sheet
labels wrap instead of being cut off.

# Release Notes — v0.58.2

## Patch: Activity heatmap and tooling refinements

Adds an activity heatmap to the web UI for visualising session activity over
time, plus supporting server and session endpoints. Includes minor tooling
adjustments across the agent loop, prompt helpers, and read/glob/grep tools,
and the corresponding unit tests.

