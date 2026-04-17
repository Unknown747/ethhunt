BINARY_DIR=bin
GENERATOR=$(BINARY_DIR)/eth-generator
CHECKER=$(BINARY_DIR)/eth-checker
HUNTER=$(BINARY_DIR)/eth-hunt

LDFLAGS=-ldflags="-s -w"

.PHONY: all build clean generator checker hunter tidy

all: build

build: tidy generator checker hunter
	@echo ""
	@echo "✅ Build selesai! Binary tersedia di $(BINARY_DIR)/"
	@ls -lh $(BINARY_DIR)/

generator:
	@mkdir -p $(BINARY_DIR)
	@echo "Building eth-generator..."
	go build $(LDFLAGS) -o $(GENERATOR) ./cmd/generator/

checker:
	@mkdir -p $(BINARY_DIR)
	@echo "Building eth-checker..."
	go build $(LDFLAGS) -o $(CHECKER) ./cmd/checker/

hunter:
	@mkdir -p $(BINARY_DIR)
	@echo "Building eth-hunt..."
	go build $(LDFLAGS) -o $(HUNTER) ./cmd/hunt/

tidy:
	@go mod tidy -e

clean:
	rm -rf $(BINARY_DIR) proxies.txt found.txt

# ── Demo ──────────────────────────────────────────────────────────
demo-gen: generator
	@echo "=== Demo: Generate 5 wallet ==="
	$(GENERATOR) -n 5

demo-check: checker
	@echo "=== Demo: Check Vitalik address ==="
	$(CHECKER) -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

demo-hunt: hunter
	@echo "=== Demo: Hunt 30 detik (Ctrl+C untuk stop) ==="
	$(HUNTER) -workers 4 -stats 3s

# ── Proxy Tools ───────────────────────────────────────────────────
fetch-proxy: hunter
	@echo "=== Fetch & validasi proxy dari internet ==="
	$(HUNTER) -fetch-proxy -workers 4 -stats 2s &
	@sleep 5 && kill %% 2>/dev/null || true

# ── Benchmark ─────────────────────────────────────────────────────
bench: generator
	@echo "=== Benchmark: 100k wallet ==="
	time $(GENERATOR) -n 100000 -workers 8 -o /dev/null
