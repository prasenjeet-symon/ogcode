# Ogweb: Chrome Web Store submission kit

Everything to paste into the CWS developer dashboard. Public copy: keep the no-em-dash
rule. After every code change to the extension, bump `version` in `manifest.json`,
then `make extension-zip` and upload the zip it prints.

## Store listing

| field | value |
|---|---|
| Name | ogweb |
| Short description (manifest, 116 chars) | Drive your Chrome tabs from ogcode over a local WebSocket bridge, limited to tabs you explicitly allow. |
| Category | Developer Tools |
| Language | English |
| Icon | 128x128 (store icon), generated from `docs/media/logo.png` into `icons/` |
| Screenshots | 1280x800 (or 640x400), 1 to 5. Suggested set: (1) the side panel with paired status and the tab allowlist, (2) an ogcode session running a browser op on a granted tab, (3) the same page showing the yellow debugger banner with a caption: "Chrome always shows you when a tab is debugged". |

Detailed description (listing body):

> ogweb connects an ogcode server running on your own machine to your real Chrome
> tabs, so an agent can see a page the way you see it: logged in, cookies included,
> no separate automation browser. Nothing happens without you: you pair the
> extension to your server with a one-time token and tick **allow** on each tab you
> want to share. The allowlist is checked again on every single operation, so
> unticking a tab locks the agent out immediately.
>
> When the agent works on a granted tab, Chrome shows its own banner that the tab
> is being debugged, and the side panel always shows the live connection state and
> the current allowlist. The extension talks to exactly one destination, the server
> origin you typed in, and never sends anything anywhere else. No analytics, no
> accounts, nothing stored beyond four small settings values.
>
> Requires ogcode running locally. Load the panel from the toolbar icon, paste the
> pair URL your ogcode server prints, press Connect, and grant the tabs to share.

## Single purpose

Bridge a user-paired ogcode server running locally to Chrome tabs the user has explicitly allowed, so the agent can view and drive those tabs.

## Permission justifications (dashboard "Why do you need this" fields)

**debugger**: Used to attach Chrome's remote debugger to tabs the user has explicitly
allowed in the extension's side panel, so the paired ogcode agent can take page
snapshots, capture screenshots, and record network and console activity on those tabs
only. Every operation re-checks the user's allowlist, and Chrome's own "started
debugging" banner stays visible while attached, so the user always sees when a tab is
being driven. The extension never attaches to a tab the user has not ticked.

**tabs**: The side panel lists the open tabs so the user can grant or revoke access per
tab by URL and title. Tab identities are also required to refuse operations that
reference a tab id that no longer exists, so a recycled tab can never be acted on.

**storage**: Persists four small values locally: the paired server origin, its pairing
token, the per-tab allowlist, and whether the user pressed Connect so the worker can
reconnect after Chrome stops it. Nothing else is stored; no data is synced.

**nativeMessaging**: Lets the extension talk to our own locally installed native
messaging host (`com.ogcode`, registered with the ogcode app) once to receive the
local pairing token without copy-paste, when nothing is paired yet. The exchange
happens only with that one host on the user's own machine and carries no page
data; stored credentials always win and the manual paste flow stays the fallback.

**sidePanel**: The pairing UI, live status line and the tab allowlist live in a side
panel, which is the extension's only user interface.

**Host permissions**: none requested. The extension performs no cross-origin requests:
its WebSocket dials only the server origin the user typed in, and all page access goes
through chrome.debugger.

## Data use disclosures

- **Collected**: Website content (snapshots, page content, screenshots, network and
  console entries), taken only from user-granted tabs and only while the user keeps
  the tab allowlisted.
- **Purpose**: Communicating with the user; the data is handed to the ogcode agent the
  user is running, on the user's own machine.
- **Off-device transmission**: Only over the paired WebSocket to the local server
  origin the user entered. Never to the publisher, never to third parties.
- **Sold / used for ads / shared**: No.

Privacy policy URL: https://ogcode.in/docs/privacy/

## Review notes (dashboard "Notes for reviewers")

> ogweb is the companion extension of ogcode, a locally-run coding agent. To
> reproduce: run the ogcode binary (it serves on a localhost port), open the side
> panel, paste the pair origin and token the server prints (`GET
> /api/browser/pair`), press Connect, and tick **allow** on a tab. The agent can then
> run snapshot/click/screenshot ops on that tab through a `chrome.debugger` CDP
> session; Chrome's debugger banner is expected and is part of the design.
> When nothing is paired yet, the extension also asks the ogcode native messaging
> host once for the local pair token; a pasted token always wins and a failed
> native attempt falls back to manual paste.
> Everything is local: the WebSocket dials only the origin the user entered, ops only
> act on allowlisted tabs, and the allowlist is re-checked per operation. The
> "debugger" permission is used for page capture on granted tabs only; there are no
> content scripts and the extension makes no cross-origin requests. A demo video is
> available on request.

## Pre-upload checklist

1. Bump `version` in `browser-extension/manifest.json` (the store rejects a repeated
   version).
2. Verify the removed `<all_urls>` on a live install once: chrome://extensions,
   reload the unpacked extension, pair, grant a tab, run navigate + snapshot + click
   + screenshot, and confirm all pass and that no op needs host access. If anything
   does, re-add the narrowest host permission that covers it, never `<all_urls>`.
3. `make extension-zip` and check the printed listing shows the manifest at the zip
   root.
4. Account: one-time $5 developer registration at the Chrome Web Store dashboard
   (unlisted or public both work; a fresh publisher account is reviewed before its
   first item goes live).
5. Fill in the listing fields above, the single purpose, the four justification
   texts, the data-use answers (Website content: collected, not shared, not off
   device except to the user's own server), and the privacy policy URL.
6. Upload the zip. `chrome.debugger` extensions draw extra scrutiny and may take
   longer than the usual review; if the review stalls, reply to the review email with
   the demo video and the review notes text.
7. After approval, publish; every later update reuses this file plus a version bump.