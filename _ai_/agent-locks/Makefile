BIN := bin/agentlocks
PREFIX ?= $(HOME)/.local/bin

.PHONY: build test fmt vet install clean analyze demo

build:
	go build -o $(BIN) ./cmd/agentlocks

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

install: build
	install -d $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/agentlocks
	@echo "installed to $(PREFIX)/agentlocks"
	@echo "run 'agentlocks install' for the settings.json snippet"

analyze: build
	$(BIN) analyze --window 10m --repos-only

demo: build
	./scripts/demo.sh

clean:
	rm -rf bin
