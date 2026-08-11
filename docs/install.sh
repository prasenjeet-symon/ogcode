#!/bin/sh
# Ogcode installer for macOS and Linux
# Usage: curl -fsSL http://ogcode.xyz/install.sh | sh

set -e

REPO="prasenjeet-symon/ogcode"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="ogcode"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    linux*)   PLATFORM="Linux" ;;
    darwin*)  PLATFORM="Darwin" ;;
    *)
        echo "Error: unsupported operating system: $OS"
        exit 1
        ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)   ARCH="x86_64" ;;
    arm64|aarch64)  ARCH="arm64" ;;
    *)
        echo "Error: unsupported architecture: $ARCH"
        exit 1
        ;;
esac

# Fetch latest release
echo "Fetching latest release..."
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')

if [ -z "$LATEST" ]; then
    echo "Error: could not determine latest release"
    exit 1
fi

# GoReleaser strips the 'v' prefix from the version in asset filenames
VERSION_NO_V=$(echo "$LATEST" | sed 's/^v//')

ASSET="${BINARY}_${VERSION_NO_V}_${PLATFORM}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST/$ASSET"

echo "Downloading ogcode $LATEST for $PLATFORM ($ARCH)..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

curl -fsSL "$URL" -o "$TMP_DIR/$ASSET"

echo "Extracting..."
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"

# Ensure the install directory exists (create with sudo if we can't write it)
if [ ! -d "$INSTALL_DIR" ]; then
    mkdir -p "$INSTALL_DIR" 2>/dev/null || sudo mkdir -p "$INSTALL_DIR"
fi

# Check if we can write to install dir
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP_DIR/$BINARY" "$INSTALL_DIR/$BINARY"
    chmod +x "$INSTALL_DIR/$BINARY"
    echo ""
    echo "ogcode $LATEST installed to $INSTALL_DIR/$BINARY"
else
    # Try with sudo
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$TMP_DIR/$BINARY" "$INSTALL_DIR/$BINARY"
    sudo chmod +x "$INSTALL_DIR/$BINARY"
    echo ""
    echo "ogcode $LATEST installed to $INSTALL_DIR/$BINARY"
fi

# Verify
echo ""
echo "✅ ogcode $LATEST is installed at $INSTALL_DIR/$BINARY"
echo "Run 'ogcode --help' to get started."
if ! command -v ogcode >/dev/null 2>&1; then
    echo ""
    echo "⚠️  $INSTALL_DIR is not in your PATH. Add it with:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

echo ""
echo "Usage:"
echo "  ogcode              # Start in Build Mode"
echo "  ogcode plan         # Start in Plan Mode"
echo "  ogcode version      # Check version"
echo ""
echo "Next step: set your AI provider API key (see README for options)."
echo ""
echo "Quick start examples:"
echo "  export ANTHROPIC_API_KEY=sk-...      # Claude"
echo "  export OPENAI_API_KEY=sk-...         # GPT"
echo "  export OPENROUTER_API_KEY=sk-...      # OpenRouter"
echo "  # Ollama Cloud:"
echo "  export OLLAMA_BASE_URL=https://api.ollama.com/v1"
echo "  export OLLAMA_API_KEY=your-key"

# ── Optional: Web Search Agent setup ────────────────────────────────────────
# The release archive already contains a search-bridge/ directory with server.js
# and package.json — extract them, then run npm install + playwright install.
echo ""
echo "Setting up web search agent..."
BRIDGE_DIR="$HOME/.local/share/ogcode/search-bridge"
mkdir -p "$BRIDGE_DIR"

# Extract bridge files from the release archive we already downloaded.
# The archive stores them under search-bridge/, so strip that leading component
# to drop server.js + package.json directly into $BRIDGE_DIR (where npm runs).
tar -xzf "$TMP_DIR/$ASSET" -C "$BRIDGE_DIR" --strip-components=1 \
    "search-bridge/server.js" "search-bridge/package.json" 2>/dev/null || true

if [ ! -f "$BRIDGE_DIR/server.js" ] || [ ! -f "$BRIDGE_DIR/package.json" ]; then
    echo "⚠️  Bridge files not found in release archive — web search unavailable."
elif command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
    ( cd "$BRIDGE_DIR" && \
      npm install --legacy-peer-deps --silent 2>/dev/null && \
      npx playwright install chromium 2>/dev/null )
    echo "✅ Web search agent ready. Enable it in ogcode Settings → General."
else
    echo "ℹ️  Node.js not found — bridge files installed but search not yet active."
    echo "   Install Node.js then run:"
    echo "     cd $BRIDGE_DIR && npm install && npx playwright install chromium"
fi
