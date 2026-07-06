.PHONY: build install install-completion test lint fmt clean

BIN := .bin/mrevdiff
INSTALL_DIR ?= $(HOME)/bin
ZSH_COMPLETION_DIR ?= /opt/homebrew/share/zsh/site-functions

build:
	@mkdir -p .bin
	go build -o $(BIN) ./cmd/mrevdiff

install: build install-completion
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/mrevdiff
	@echo "installed $(INSTALL_DIR)/mrevdiff"

install-completion:
	@if [ -d "$(ZSH_COMPLETION_DIR)" ]; then \
		install -m 0644 completions/_mrevdiff $(ZSH_COMPLETION_DIR)/_mrevdiff; \
		echo "installed $(ZSH_COMPLETION_DIR)/_mrevdiff"; \
	else \
		echo "skip: $(ZSH_COMPLETION_DIR) not found (set ZSH_COMPLETION_DIR= to override)"; \
	fi

test:
	go test -cover ./...

lint:
	go vet ./...

fmt:
	gofmt -w cmd pkg

clean:
	rm -rf .bin
