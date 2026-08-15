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

install: build
	mkdir -p $(HOME)/.local/bin
	cp ogcode $(HOME)/.local/bin/ogcode
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