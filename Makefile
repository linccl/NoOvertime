SHELL := /usr/bin/env bash

.PHONY: regression test check gate fmt-check vet unit-test

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
	unformatted="$$(gofmt -l $$files)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Found unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

unit-test:
	go test ./...

gate: fmt-check vet unit-test check
