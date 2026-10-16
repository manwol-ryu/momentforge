# MomentForge build. Run `make help` for targets.

GO      ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
GOFMT   ?= $(dir $(GO))gofmt
NPM     ?= npm
PYTHON  ?= python3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/momentforge
LDFLAGS := -s -w -X main.version=$(VERSION)

E2E_PORT := 18765
E2E_VENV := e2e/.venv
E2E_PY   := $(E2E_VENV)/bin/python

.PHONY: help dev dev-server dev-web web build check test e2e e2e-deps clean

help:
	@echo "make dev    Go 서버(8765) + Vite 개발 서버(5173) 동시 실행"
	@echo "make build  화면 빌드 후 단일 실행파일 $(BIN) 생성"
	@echo "make test   구조 검사 + gofmt + go vet + go test + 화면 타입 검사"
	@echo "make e2e    build 후 Playwright 정상 작동 테스트"
	@echo "make clean  빌드 결과 삭제"

web/node_modules/.installed: web/package.json web/package-lock.json
	cd web && $(NPM) ci --no-fund --no-audit
	@touch $@

# go:embed needs web/dist to exist; a placeholder lets Go build before the web build.
web/dist:
	@mkdir -p web/dist && touch web/dist/.placeholder

web: web/node_modules/.installed
	cd web && $(NPM) run build

build: web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/momentforge

check: web/dist
	$(PYTHON) -I .claude/skills/momentforge-architecture/scripts/check_layers.py
	@out="$$($(GOFMT) -l cmd internal web/embed.go)"; \
		if [ -n "$$out" ]; then echo "gofmt가 필요한 파일:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...

test: check web/node_modules/.installed
	$(GO) test ./...
	cd web && $(NPM) run typecheck

dev: web/dist web/node_modules/.installed
	@echo "개발 서버: http://127.0.0.1:5173 (API는 Go 서버 127.0.0.1:8765로 전달)"
	@$(MAKE) -j2 dev-server dev-web

dev-server:
	$(GO) run ./cmd/momentforge serve --no-browser

dev-web:
	@sleep 1
	cd web && $(NPM) run dev

$(E2E_VENV)/.installed: e2e/requirements.txt
	$(PYTHON) -m venv $(E2E_VENV)
	$(E2E_PY) -m pip install -q --upgrade pip
	$(E2E_PY) -m pip install -q -r e2e/requirements.txt
	$(E2E_PY) -m playwright install chromium
	@touch $@

e2e-deps: $(E2E_VENV)/.installed

e2e: build e2e-deps
	rm -rf e2e/.data
	$(E2E_PY) e2e/with_server.py \
		--server "./$(BIN) serve --no-browser --port $(E2E_PORT) --data e2e/.data" --port $(E2E_PORT) \
		-- $(E2E_PY) e2e/run.py --base-url http://127.0.0.1:$(E2E_PORT)

clean:
	rm -rf bin web/dist e2e/.data
