BINARY_DIR=bin
GENERATOR_BIN=$(BINARY_DIR)/eth-generator
CHECKER_BIN=$(BINARY_DIR)/eth-checker

.PHONY: all build clean test generator checker tidy

all: build

build: tidy generator checker
	@echo "Build complete! Binaries in $(BINARY_DIR)/"

generator:
	@mkdir -p $(BINARY_DIR)
	@echo "Building eth-generator..."
	go build -ldflags="-s -w" -o $(GENERATOR_BIN) ./cmd/generator/

checker:
	@mkdir -p $(BINARY_DIR)
	@echo "Building eth-checker..."
	go build -ldflags="-s -w" -o $(CHECKER_BIN) ./cmd/checker/

tidy:
	go mod tidy

clean:
	rm -rf $(BINARY_DIR)

test-generator: generator
	@echo "=== Testing Generator (5 wallets) ==="
	$(GENERATOR_BIN) -n 5

test-checker: checker
	@echo "=== Testing Checker (Vitalik's address) ==="
	$(CHECKER_BIN) -addr 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

bench-generator: generator
	@echo "=== Benchmark: 10000 wallets ==="
	time $(GENERATOR_BIN) -n 10000 -workers 8 -o /dev/null
