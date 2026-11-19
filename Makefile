GO      ?= go
BIN     ?= turislab
APP     ?= app
UI      ?= ui
CONFIG  ?= app/config.turislab.yaml
PORT    ?= 8090
MCP_BROWSER_PORT ?= 8931
ARTIFACTS_DIR ?= $(APP)/artifacts
CHROME  ?= /usr/bin/chromium
MCP_BROWSER_FLAGS = --port $(MCP_BROWSER_PORT) --host 127.0.0.1 --headless \
	--executable-path $(CHROME) --viewport-size 1365x768 \
	--image-responses omit --ignore-roots --idle-timeout 0
TL_SCRAPER_VERSION ?= v1.18.1
TL_SCRAPER_BIN ?= bin/google-maps-scraper

.DEFAULT_GOAL := help
.PHONY: help all build test vet fmt lint run scan-once validate serve tidy clean datasets \
	ui-install ui-dev ui-check ui-build ui-build-if-needed turislab-scraper mcp-browser

help: ## Show the available targets
	@echo "usage: make [target]"
	@echo
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "  variables:"
	@echo "    CONFIG=$(CONFIG) PORT=$(PORT)"

all: build ## Compile the app

build: ui-build-if-needed ## Compile the turislab CLI
	$(GO) -C $(APP) build -o ../$(BIN) ./cmd/turislab

test: ## Run the Go tests
	$(GO) -C $(APP) test ./...

vet: ## Static analysis (go vet)
	$(GO) -C $(APP) vet ./...

fmt: ## gofmt the Go sources
	gofmt -l -w $(APP)

lint: fmt vet test ## Everything CI checks, no extra tooling

tidy: ## Sync go.mod / go.sum
	$(GO) -C $(APP) mod tidy

# Run everything with one command: the browser MCP sidecar is started in the
# background (unless one is already listening) so automatic captures work by
# default, and is stopped when the app exits.
run: build ## Run browser MCP + scanner + API + UI on :$(PORT)
	@code=$$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$(MCP_BROWSER_PORT)/mcp || true); \
	if [ "$$code" = "000" ]; then \
		mkdir -p $(ARTIFACTS_DIR); \
		(cd $(ARTIFACTS_DIR) && exec npx -y @playwright/mcp@latest $(MCP_BROWSER_FLAGS) > mcp-browser.log 2>&1) & \
		browser_pid=$$!; \
		trap 'kill $$browser_pid 2>/dev/null || true' EXIT INT TERM; \
		echo "browser MCP started on :$(MCP_BROWSER_PORT) (pid $$browser_pid)"; \
		ready=no; \
		for i in $$(seq 1 30); do \
			probe=$$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$(MCP_BROWSER_PORT)/mcp || true); \
			if [ "$$probe" != "000" ]; then ready=yes; break; fi; \
			sleep 1; \
		done; \
		if [ "$$ready" = "no" ]; then echo "browser MCP no respondió en :$(MCP_BROWSER_PORT): las capturas fallarán" >&2; fi; \
	else \
		echo "reusing browser MCP on :$(MCP_BROWSER_PORT)"; \
	fi; \
	./$(BIN) --config $(CONFIG) --addr :$(PORT)

scan-once: build ## Scan every establishment once and exit
	./$(BIN) --config $(CONFIG) --once

validate: build ## Compare scores with input.ground_truth_path (set it in the config)
	./$(BIN) --config $(CONFIG) --validate

# Regenerate app/datasets/lugo.csv from the official REAT directories.
# Defaults: Lugo municipality, 500 m around La Oca, restaurants on the
# surrounding streets. Override with REAT_FLAGS="-concello Vigo -max-alox 20".
REAT_FLAGS ?= -out datasets/lugo.csv
datasets: ## Rebuild app/datasets/*.csv from the REAT directories
	$(GO) -C $(APP) run ./cmd/reat2csv $(REAT_FLAGS)

# The gosom scraper is an MIT-licensed standalone binary (no API key). The
# release is ~70 MB, so it is gitignored and fetched on demand. Its first run
# also downloads the Playwright Chromium build it drives.
turislab-scraper: ## Download the gosom Google Maps scraper into bin/
	@if [ -x "$(TL_SCRAPER_BIN)" ]; then \
		echo "$(TL_SCRAPER_BIN) already present"; exit 0; \
	fi; \
	os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); \
	case "$$os" in linux) os=linux ;; darwin) os=darwin ;; *) echo "unsupported OS: $$os (build from source)"; exit 1 ;; esac; \
	case "$$arch" in x86_64|amd64) arch=amd64 ;; *) echo "no prebuilt binary for $$arch; build from source"; exit 1 ;; esac; \
	ver=$$(echo "$(TL_SCRAPER_VERSION)" | sed 's/^v//'); \
	url="https://github.com/gosom/google-maps-scraper/releases/download/$(TL_SCRAPER_VERSION)/google_maps_scraper-$$ver-$$os-$$arch"; \
	echo "downloading $$url"; \
	mkdir -p $(dir $(TL_SCRAPER_BIN)); \
	curl -fsSL -o "$(TL_SCRAPER_BIN)" "$$url"; \
	chmod +x "$(TL_SCRAPER_BIN)"; \
	echo "installed $(TL_SCRAPER_BIN)"

# Playwright MCP sidecar: the agent drives this browser to read websites and
# save screenshots. Run it in another terminal before asking the chat for a
# capture. --image-responses omit keeps PNGs out of the chat history; the
# explicit filename passed by the agent resolves against this working dir.
mcp-browser: ## Start the browser MCP on :$(MCP_BROWSER_PORT) (captures in $(ARTIFACTS_DIR))
	@mkdir -p $(ARTIFACTS_DIR)
	cd $(ARTIFACTS_DIR) && npx -y @playwright/mcp@latest $(MCP_BROWSER_FLAGS)

serve: clean ## Clean, rebuild everything (UI included) and serve on :$(PORT)
	@$(MAKE) run

clean: ## Remove the binary, the embedded UI build and the local database
	rm -f $(BIN)
	rm -rf $(APP)/ui/build
	# The seeder upserts, so a stale database would mix datasets (e.g.
	# Madrid leftovers into a Lugo-only report); drop it on every clean.
	rm -f $(APP)/turislab.db $(APP)/turislab.db-shm $(APP)/turislab.db-wal
	# go:embed needs the folder to exist; restore the tracked placeholder
	# that the next UI build replaces with the real bundle.
	mkdir -p $(APP)/ui/build
	cp $(APP)/ui/placeholder.html $(APP)/ui/build/placeholder.html

# The embed folder app/ui/build holds the compiled UI (or the tracked
# placeholder on a clean clone). Rebuild the UI only when the real bundle
# is not there yet; after that, `go build` works on its own.
ui-build-if-needed: ## Build the web UI only if it has not been built yet
	@if [ ! -d $(APP)/ui/build/_app ]; then $(MAKE) ui-build; else echo "web UI already built ($(APP)/ui/build)"; fi

# --- web UI ---------------------------------------------------------------
# SvelteKit app (ui/) that renders the scan report. It reads the JSON from
# the API endpoints; the dev server proxies /api to the Go server on PORT.
# Its build writes straight into app/ui/build, the Go embed folder.

ui-install: ## Install the web UI npm dependencies
	cd $(UI) && npm install

ui-dev: ## Start the web UI dev server on :5173
	cd $(UI) && DOCTOR_URL=http://localhost:$(PORT) npm run dev

ui-check: ## Type-check the web UI
	cd $(UI) && npm run check

ui-build: ## Build the web UI bundle (embedded into the binary)
	cd $(UI) && npm run build
	# The adapter wipes app/ui/build; restore the tracked placeholder that
	# keeps go:embed compiling on a clean clone.
	cp $(APP)/ui/placeholder.html $(APP)/ui/build/placeholder.html
