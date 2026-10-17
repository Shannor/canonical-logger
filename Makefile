.PHONY: help work-init test tidy bump tag push-tags list-adapters

# Automatically discover all adapter submodules containing a go.mod
ADAPTER_DIRS := $(patsubst %/go.mod,%,$(wildcard adapter/*/go.mod))

help: ## Show available make targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

work-init: ## Initialize or sync local go.work across root and all adapters
	@if [ ! -f go.work ]; then go work init .; fi
	@go work use . $(ADAPTER_DIRS)
	@echo "Workspace initialized with modules: . $(ADAPTER_DIRS)"

test: ## Run tests with race detector across root and all adapter modules
	go test -race ./...
	@for dir in $(ADAPTER_DIRS); do \
		echo "==> Testing $$dir"; \
		(cd $$dir && go test -race ./...) || exit 1; \
	done

tidy: ## Run go mod tidy across root and all adapter modules
	go mod tidy
	@for dir in $(ADAPTER_DIRS); do \
		echo "==> Tidying $$dir"; \
		(cd $$dir && go mod tidy) || exit 1; \
	done

list-adapters: ## List discovered adapter modules
	@for dir in $(ADAPTER_DIRS); do echo $$dir; done

bump: ## Update canonical-logger version in all adapter go.mod files (Usage: make bump VERSION=v0.3.0)
	@if [ -z "$(VERSION)" ]; then echo "Error: VERSION is required (e.g. make bump VERSION=v0.3.0)"; exit 1; fi
	@for dir in $(ADAPTER_DIRS); do \
		echo "==> Bumping github.com/Shannor/canonical-logger to $(VERSION) in $$dir/go.mod"; \
		(cd $$dir && go mod edit -require=github.com/Shannor/canonical-logger@$(VERSION)); \
	done

tag: ## Create lockstep git tags for root and all adapter modules (Usage: make tag VERSION=v0.3.0)
	@if [ -z "$(VERSION)" ]; then echo "Error: VERSION is required (e.g. make tag VERSION=v0.3.0)"; exit 1; fi
	@echo "==> Tagging root module: $(VERSION)"
	git tag $(VERSION)
	@for dir in $(ADAPTER_DIRS); do \
		echo "==> Tagging adapter module: $$dir/$(VERSION)"; \
		git tag "$$dir/$(VERSION)"; \
	done
	@echo "Created tags for $(VERSION). Run 'make push-tags VERSION=$(VERSION)' to push to origin."

push-tags: ## Push lockstep git tags for root and all adapters to origin (Usage: make push-tags VERSION=v0.3.0)
	@if [ -z "$(VERSION)" ]; then echo "Error: VERSION is required (e.g. make push-tags VERSION=v0.3.0)"; exit 1; fi
	git push origin $(VERSION) $(foreach dir,$(ADAPTER_DIRS),$(dir)/$(VERSION))
