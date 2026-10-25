.PHONY: build test check python-test desktop desktop-macos desktop-linux desktop-windows desktop-check plugin-check
build:
	CGO_ENABLED=1 go build -trimpath -o bin/ai-worker-mcp ./cmd/ai-worker-mcp

test:
	$(MAKE) python-test
	go test -race ./...

check:
	go vet ./...
	$(MAKE) python-test
	go test -race ./...

python-test:
	python3 -m unittest discover -s scripts -p 'test_*.py'

plugin-check:
	python3 scripts/validate_codex_plugin.py

# Keep the Wails toolchain isolated from the MCP Go module.
WAILS ?=
desktop:
	@case "$$(go env GOHOSTOS)" in \
	  darwin) $(MAKE) desktop-macos ;; \
	  linux) $(MAKE) desktop-linux ;; \
	  windows) $(MAKE) desktop-windows ;; \
	  *) echo "Unsupported build host" >&2; exit 1 ;; \
	esac

desktop-macos:
	WAILS="$(WAILS)" bash scripts/build-macos.sh

desktop-linux:
	WAILS="$(WAILS)" bash scripts/build-linux.sh

desktop-windows:
	WAILS="$(WAILS)" bash scripts/build-windows.sh

desktop-check:
	cd desktop/frontend && npm run build && npm test
	cd desktop && go vet ./... && go test ./...
