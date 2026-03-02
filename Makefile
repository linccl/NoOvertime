SHELL := /usr/bin/env bash

.PHONY: regression test check gate fmt-check vet unit-test

# Work around dyld abort on recent macOS where Go's default internal linker may
# produce *.test binaries missing LC_UUID; external link mode fixes it.
UNIT_TEST_FLAGS :=
ifeq ($(shell uname -s),Darwin)
UNIT_TEST_FLAGS += -ldflags=-linkmode=external
endif

regression:
	./scripts/run_regression_gate.sh $(REGRESSION_GATE_ARGS)

test: regression

check: regression

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './vendor/*')"; \
	if [ -z "$$files" ]; then \
		echo "No Go files found."; \
		exit 0; \
	fi; \
	command -v gofmt >/dev/null 2>&1 || { echo "gofmt not found"; exit 1; }; \
	unformatted="$$(gofmt -l $$files)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Found unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

unit-test:
	go test $(UNIT_TEST_FLAGS) ./...

gate: fmt-check vet unit-test check
