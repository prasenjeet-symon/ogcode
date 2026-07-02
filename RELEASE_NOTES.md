# Release Notes — v0.13.5

## UI/UX Redesign & HTML Widget Improvements

This patch release brings a comprehensive visual refresh inspired by Linear's
design language, plus a fix that makes HTML widgets rendered in chat feel like
a natural part of the conversation rather than separate cards.

---

### ✨ Style: Linear-Inspired UI/UX Redesign

The entire color system, typography, and animation framework has been overhauled
for a more polished, modern feel:

- **Color system:** Accent shifted from blue to Linear violet across all design
  tokens and Go backend constants
- **Typography:** Tighter body text (13px), tighter line-height, slight negative
  tracking for a refined feel
- **Surfaces:** Warm-tinted dark grays at each elevation level instead of flat
  grays
- **Shadows:** Softer, layered depth replacing heavy single shadows; accent-glow
  shadows on focus and active states
- **Animations:** Spring-based timing curves replace linear ease-out; new
  animation classes — `scale-in`, `pop-in`, `slide-down`, `stagger`
- **Micro-interactions:** Hover and active scale on buttons and chips; soft glow
  ring focus states replacing hard outlines
- **Scrollbars:** Ultra-thin 6px, barely visible until hover
- **Command menu:** Backdrop blur, pop-in animation, accent glow shadow
- **Prompt input:** Glow-on-focus with accent-tinted shadow
- **Session sidebar:** Spring transitions, active dot with pulse-ring, tighter
  header
- **Messages:** Directional slide-in with spring curves
- **Links:** Shifted from blue to violet to match the new accent
- **Settings:** Linear Violet as the first color preset option

### 🐛 Bug Fixes

- **HTML widget:** Removed forced dark background and rounded border from the
  chat iframe. HTML/CSS/JS code blocks now render with a transparent background
  and zero border, blending seamlessly into the conversation flow instead of
  appearing as separate cards. Updated agent prompt to instruct against adding
  background colors or card-like containers.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.13.4...v0.13.5*