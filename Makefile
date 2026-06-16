SHELL := /bin/sh
MODULES := go-meshapi-client run-controller tf-block-runner
RUN_CONTROLLER_CONFIG := run-controller/runner-config.yml
TF_BLOCK_RUNNER_CONFIG := tf-block-runner/runner-config.yml

.PHONY: help start-run-controller start-tf-block-runner test test-run-controller test-tf-block-runner fmt vet tidy work-sync

define run-in-modules
	@set -e; \
	for module in $(MODULES); do \
		echo "==> $$module: $(1)"; \
		(cd $$module && $(1)); \
	done
endef

help:
	@echo "Available targets:"
	@echo "  start-run-controller    Run run-controller"
	@echo "  start-tf-block-runner   Run tf-block-runner"
	@echo "  test                  Run all tests"
	@echo "  test-run-controller   Run run-controller tests"
	@echo "  test-tf-block-runner  Run tf-block-runner tests"
	@echo "  fmt                   Format all Go code"
	@echo "  vet                   Run go vet on all modules"
	@echo "  tidy                  Tidy modules"
	@echo "  work-sync             Sync go.work entries"

start-run-controller:
	RUNCONTROLLER_CONFIG_FILE=$(RUN_CONTROLLER_CONFIG)	go run ./run-controller

start-tf-block-runner:
	RUNNER_CONFIG_FILE=$(TF_BLOCK_RUNNER_CONFIG) go run ./tf-block-runner

test:
	$(call run-in-modules,go test ./...)

test-run-controller:
	cd run-controller && go test ./...

test-tf-block-runner:
	cd tf-block-runner && go test ./...

fmt:
	$(call run-in-modules,go fmt ./...)

vet:
	$(call run-in-modules,go vet ./...)

tidy:
	$(call run-in-modules,go mod tidy)

work-sync:
	go work sync
