BINARY    := bin/kittypaw
PKG       := ./cli
BUILDTIME := $(shell TZ=Asia/Seoul date +%Y-%m-%d\ %H:%M\ KST)
LDFLAGS   := -X 'main.version=dev ($(BUILDTIME))'

.PHONY: build test test-unit test-integration test-e2e test-ci lint fmt run clean eval-secretary eval-user-flows eval-local smoke dev-models dev-models-setup dev-models-server dev-models-chat dev-models-stop dev-models-clean dev-models-status dev-models-tunnel-ollama dev-models-tunnel-ollama-stop dev-models-tunnel-ollama-status dev-models-tunnel-lms dev-models-tunnel-lms-stop dev-models-tunnel-lms-status

build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test: test-ci

test-ci:
	go test ./... -v -count=1
	go test -tags integration ./... -v -count=1
	go test -tags e2e ./... -v -count=1
	golangci-lint run ./...
	$(MAKE) build

test-unit:
	go test ./... -v -count=1 -short

test-integration:
	go test -tags integration ./... -v -count=1

test-e2e:
	go test -tags e2e ./... -v -count=1

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

run: build
	./$(BINARY)

eval-secretary:
	eval/secretary_smoke/run.sh

eval-user-flows:
	eval/user_vision_flows/run.sh

eval-local: eval-secretary eval-user-flows

smoke: eval-local

clean:
	rm -f $(BINARY)

# Local /model command demo harness — isolated via KITTYPAW_CONFIG_DIR so
# the user's real ~/.kittypaw/ daemon is untouched. See scripts/dev-models.sh.
# Recommended single-shot entry: `make dev-models`.
dev-models: build
	scripts/dev-models.sh go

dev-models-setup:
	scripts/dev-models.sh setup

dev-models-server: build
	scripts/dev-models.sh server

dev-models-chat:
	scripts/dev-models.sh chat

dev-models-stop:
	scripts/dev-models.sh stop

dev-models-clean:
	scripts/dev-models.sh clean --yes

dev-models-status:
	scripts/dev-models.sh status

# SSH-tunneled local backends on emac (M3 Pro 36GB). The M1 Air runs the
# chat client; emac runs ollama serve and/or LM Studio (Apple Metal MLX).
# Two independent tunnels with separate ControlPath sockets so they can
# coexist:
#   ollama → localhost:11500 → emac:11434  (`ollama serve`)
#   lms    → localhost:11600 → emac:1234   (LM Studio HTTP server, no auth)
# Prereqs: `ssh emac` alias in ~/.ssh/config; ollama installed on emac
# (for ollama tunnel); LM Studio app running with HTTP server enabled +
# target model loaded via GUI (for lms tunnel). See docs/DEV_MODELS.md.
dev-models-tunnel-ollama:
	scripts/dev-models.sh tunnel-ollama-start

dev-models-tunnel-ollama-stop:
	scripts/dev-models.sh tunnel-ollama-stop

dev-models-tunnel-ollama-status:
	scripts/dev-models.sh tunnel-ollama-status

dev-models-tunnel-lms:
	scripts/dev-models.sh tunnel-lms-start

dev-models-tunnel-lms-stop:
	scripts/dev-models.sh tunnel-lms-stop

dev-models-tunnel-lms-status:
	scripts/dev-models.sh tunnel-lms-status
