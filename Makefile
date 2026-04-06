# Sharko — Makefile

.PHONY: help demo dev build test test-go test-ui lint ui-build ui-install clean build-go release

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

demo: ## Build UI + start server in demo mode
	@# Kill any existing sharko demo server
	@-pkill -f "sharko serve --demo" 2>/dev/null || true
	@sleep 0.5
	@echo ""
	@echo "  🦈 Sharko Demo Mode"
	@echo "  Building UI..."
	@rm -rf ui/dist
	@cd ui && npm run build 2>&1 | grep -v "PLUGIN_TIMINGS\|chunks are larger\|dynamic import\|codeSplitting\|chunkSizeWarningLimit\|rolldown.rs"
	@echo "  Open http://localhost:$(PORT)"
	@echo "  Login: admin/admin (admin) or qa/sharko (viewer)"
	@echo ""
	go run ./cmd/sharko serve --demo --port $(PORT) --static ui/dist

dev: ## Start backend (demo) + frontend with hot reload
	@-pkill -f "sharko serve --demo" 2>/dev/null || true
	@sleep 0.5
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

release: ## Tag and push a release (usage: make release VERSION=1.0.0)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=1.0.0"; exit 1; fi
	@echo ""
	@echo "  🦈 Sharko Release v$(VERSION)"
	@echo ""
	@echo "$(VERSION)" > version.txt
	@sed -i '' 's/^version:.*/version: $(VERSION)/' charts/sharko/Chart.yaml
	@sed -i '' 's/^appVersion:.*/appVersion: "$(VERSION)"/' charts/sharko/Chart.yaml
	@if git diff --quiet version.txt charts/sharko/Chart.yaml; then \
		echo "  Version already set to $(VERSION) — tagging current main"; \
		git tag -a "v$(VERSION)" -m "Release v$(VERSION)"; \
	else \
		git checkout -b release/v$(VERSION); \
		git add -f version.txt charts/sharko/Chart.yaml; \
		git commit -m "release: v$(VERSION)"; \
		git push -u origin release/v$(VERSION); \
		gh pr create --title "release: v$(VERSION)" --body "Version bump to $(VERSION)"; \
		gh pr merge --squash; \
		git checkout main; \
		git pull origin main; \
		git tag -a "v$(VERSION)" -m "Release v$(VERSION)"; \
	fi
	@echo ""
	@echo "  ✅ Tagged v$(VERSION). Push the tag:"
	@echo "    git push origin v$(VERSION)"
	@echo ""
