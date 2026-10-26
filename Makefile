.PHONY: dev build clean install dev-blog build-blog extension-zip

dev:
	@echo "Starting Go server on :9595..."
	go run . serve --port 9595

dev-web:
	@echo "Starting Vite dev server on :5173..."
	cd web && npm run dev

build-web:
	cd web && npm install --legacy-peer-deps --cache /tmp/npm-cache && npm run build

dev-blog:
	@echo "Starting Astro dev server on :4321 (blog at /blog/)..."
	cd blog && npm install --cache /tmp/npm-cache && npm run dev

build-blog:
	cd blog && npm install --cache /tmp/npm-cache && npm run build

build-server:
	$(eval VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || node -p "require('./web/package.json').version" 2>/dev/null || echo "dev"))
	CGO_ENABLED=1 go build \
		-ldflags "-X github.com/prasenjeet-symon/ogcode/internal/version.Version=$(VERSION) -X github.com/prasenjeet-symon/ogcode/internal/provider.OGXAppSecret=$(OGX_APP_SECRET)" \
		-o ogcode .

build: build-web build-server
	@echo "Build complete: ./ogcode"

# On macOS, replacing the binary in place leaves the kernel with a stale cached
# code signature for the path and it SIGKILLs the new process ("Killed: 9").
# Removing the old file first (fresh inode) and re-signing ad-hoc avoids it.
install: build
	mkdir -p $(HOME)/.local/bin
	rm -f $(HOME)/.local/bin/ogcode
	cp ogcode $(HOME)/.local/bin/ogcode
	@if [ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then \
		codesign --force --sign - $(HOME)/.local/bin/ogcode && echo "Re-signed ogcode (macOS ad-hoc)" \
			|| echo "warning: codesign failed; if ogcode is Killed:9, run: codesign --force --sign - $(HOME)/.local/bin/ogcode"; \
	fi
	@echo "Installed to $(HOME)/.local/bin/ogcode"
	@echo "Web search is built in — no Node.js or Chromium needed."

clean:
	rm -f ogcode
	rm -rf web/dist web/node_modules web/.solid
	rm -rf blog/node_modules blog/.astro docs/blog
	rm -rf .ogcode

test:
	go test ./...

# Zip the extension for a Chrome Web Store upload. The manifest must sit at
# the ZIP ROOT (never inside a nested folder), so zip the files, not the
# directory. Bump "version" in browser-extension/manifest.json before every
# re-upload; the store rejects a version that already exists.
extension-zip:
	$(eval EXT_VERSION := $(shell node -p "require('./browser-extension/manifest.json').version"))
	cd browser-extension && rm -f ../ogweb-$(EXT_VERSION).zip && zip -X ../ogweb-$(EXT_VERSION).zip manifest.json background.js sidepanel.html sidepanel.js sidepanel.css README.md PROTOCOL.md icons/*
	@unzip -l ogweb-$(EXT_VERSION).zip | head -20
	@echo "Ready for upload: ogweb-$(EXT_VERSION).zip"