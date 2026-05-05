BINARY    := bin/kittypaw
PKG       := ./cli
BUILDTIME := $(shell TZ=Asia/Seoul date +%Y-%m-%d\ %H:%M\ KST)
LDFLAGS   := -X 'main.version=dev ($(BUILDTIME))'

.PHONY: build test test-unit test-integration test-e2e test-ci lint fmt run clean eval-secretary eval-user-flows eval-local smoke dev-models-setup dev-models-server dev-models-chat dev-models-stop dev-models-clean dev-models-status dev-models-tunnel dev-models-tunnel-stop dev-models-tunnel-status dev-models-ollama-measure

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

# Local /model command demo harness — isolated KittyPaw HOME so the user's
# real ~/.kittypaw/ daemon is untouched. See scripts/dev-models.sh.
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

# SSH-tunneled ollama on a separate "emac" host. The local M1 Air runs
# the chat client; emac (M3 Pro 36GB) runs `ollama serve`. We forward
# emac:11434 → localhost:11434 over an OpenSSH ControlMaster session.
# Prereqs: `ssh emac` alias in ~/.ssh/config, ollama installed on emac,
# and (for measure) jq + bats. See docs/DEV_MODELS.md for the full flow.
dev-models-tunnel:
	scripts/dev-models.sh tunnel-start

dev-models-tunnel-stop:
	scripts/dev-models.sh tunnel-stop

dev-models-tunnel-status:
	scripts/dev-models.sh tunnel-status

# One-shot measurement against an ollama model on emac. Pulls the model,
# config-swaps [llm].default + reloads the daemon, fires one chat round,
# prints latency + response, then a trap restores the original config.
# Usage: make dev-models-ollama-measure MODEL=qwen2.5:7b [PROMPT='...']
dev-models-ollama-measure: build
	scripts/dev-models-ollama-measure.sh $(MODEL) $(PROMPT)
