.PHONY: dev build clean

dev:
	@echo "Starting Go server on :9595..."
	go run . serve --port 9595

dev-web:
	@echo "Starting Vite dev server on :5173..."
	cd web && npm run dev

build-web:
	cd web && npm install --legacy-peer-deps --cache /tmp/npm-cache && npm run build

build-server:
	$(eval VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || node -p "require('./web/package.json').version" 2>/dev/null || echo "dev"))
	CGO_ENABLED=1 go build \
		-ldflags "-X github.com/prasenjeet-symon/ogcode/internal/version.Version=$(VERSION) \
		          -X github.com/prasenjeet-symon/ogcode/internal/cli.version=$(VERSION)" \
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
	mkdir -p $(HOME)/.local/share/ogcode/search-bridge
	cp tools/search-bridge/package.json tools/search-bridge/server.js $(HOME)/.local/share/ogcode/search-bridge/
	cd $(HOME)/.local/share/ogcode/search-bridge && npm install --legacy-peer-deps --cache /tmp/npm-cache
	cd $(HOME)/.local/share/ogcode/search-bridge && npx playwright install chromium
	@echo "Search bridge installed to $(HOME)/.local/share/ogcode/search-bridge"

clean:
	rm -f ogcode
	rm -rf web/dist web/node_modules web/.solid
	rm -rf .ogcode

test:
	go test ./...