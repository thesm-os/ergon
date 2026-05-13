.PHONY: help bootstrap install fmt license generate build clean \
        lint lint-md tidy check-tidy \
        test test-race test-bench test-fuzz test-coverage \
        bench-baseline bench-regression \
        check check-coverage check-mutation check-vuln \
        release

# ergon dogfoods itself. `go run` keeps the dev loop snappy
# (cached after the first run); CI uses `go install ./cmd/ergon`
# so the binary lives on PATH.
ERGON ?= go run ./cmd/ergon

help:               ; @$(ERGON) help

bootstrap:          ; $(ERGON) bootstrap
install:            ; $(ERGON) mod install
fmt:                ; $(ERGON) fmt
license:            ; $(ERGON) license
generate:           ; $(ERGON) generate
build:              ; $(ERGON) build
clean:              ; $(ERGON) clean

lint:               ; $(ERGON) lint
lint-md:            ; $(ERGON) lint md

tidy:               ; $(ERGON) mod tidy
check-tidy:         ; $(ERGON) mod verify

test:               ; $(ERGON) test
test-race:          ; $(ERGON) test race
test-bench:         ; $(ERGON) test bench
test-fuzz:          ; $(ERGON) test fuzz
test-coverage:      ; $(ERGON) test coverage

bench-baseline:     ; $(ERGON) bench baseline
bench-regression:   ; $(ERGON) bench regression

check:              ; $(ERGON) check
check-coverage:     ; $(ERGON) check coverage
check-mutation:     ; $(ERGON) check mutation
check-vuln:         ; $(ERGON) check vuln

release:            ; $(ERGON) release $(if $(MESSAGE),-m "$(MESSAGE)",) $(FLAGS)

.DEFAULT_GOAL := help
