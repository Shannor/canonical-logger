.PHONY: help test tidy tag push-tag release

help: ## Show available make targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

test: ## Run all unit tests with race detector
	go test -race ./...

tidy: ## Run go mod tidy
	go mod tidy

tag: ## Create a git tag for a release (Usage: make tag VERSION=v0.3.0)
	@if [ -z "$(VERSION)" ]; then echo "Error: VERSION is required (e.g. make tag VERSION=v0.3.0)"; exit 1; fi
	git tag -f $(VERSION)
	@echo "Created tag $(VERSION). Run 'make push-tag VERSION=$(VERSION)' to push to origin."

push-tag: ## Push a release tag to origin (Usage: make push-tag VERSION=v0.3.0)
	@if [ -z "$(VERSION)" ]; then echo "Error: VERSION is required (e.g. make push-tag VERSION=v0.3.0)"; exit 1; fi
	git push -f origin $(VERSION)

release: test tag push-tag ## Run tests, create tag, and push tag to origin (Usage: make release VERSION=v0.3.0)
