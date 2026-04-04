# Sharko — Makefile

.PHONY: help demo dev build test test-go test-ui lint ui-build ui-install clean build-go

PORT ?= 8080

help: ## Show available targets
	@echo ""
	@echo "  🦈 Sharko"
	@echo ""
	@echo "  Quick Start:"
	@echo "    make demo         Build UI + start with mock backends (http://localhost:$(PORT))"
	@echo "    make dev          Hot-reload dev mode (http://localhost:5173)"
	@echo ""
	@echo "  Build & Test:"
	@echo "    make build        Build Go binary + UI"
	@echo "    make test         Run all tests (Go + UI)"
	@echo "    make lint         Go vet + UI build check"
	@echo ""

demo: ui-build ## Build UI + start server in demo mode
	@echo ""
	@echo "  🦈 Sharko Demo Mode"
	@echo "  Open http://localhost:$(PORT)"
	@echo "  Login: admin/admin (admin) or qa/sharko (viewer)"
	@echo ""
	go run ./cmd/sharko serve --demo --port $(PORT) --static ui/dist

dev: ## Start backend (demo) + frontend with hot reload
	@echo ""
	@echo "  🦈 Sharko Dev Mode (hot reload)"
	@echo "  Backend: http://localhost:$(PORT) (demo mode)"
	@echo "  Frontend: http://localhost:5173 ← open this"
	@echo "  Login: admin/admin or qa/sharko"
	@echo ""
	@trap 'kill 0' EXIT; \
		go run ./cmd/sharko serve --demo --port $(PORT) & \
		cd ui && npm run dev & \
		wait

build: ui-build build-go ## Build Go binary + UI

build-go: ## Build Go binary
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/sharko ./cmd/sharko
	@echo "Built: bin/sharko"

ui-build: ## Build the React UI
	cd ui && npm run build

ui-install: ## Install UI dependencies
	cd ui && npm install

test: test-go test-ui ## Run all tests

test-go: ## Run Go tests
	go clean -testcache
	go test ./...

test-ui: ## Run UI tests
	cd ui && npm test -- --run

lint: ## Go vet + UI build check
	go vet ./...
	cd ui && npm run build

clean: ## Remove build artifacts
	rm -rf bin/ ui/dist/
