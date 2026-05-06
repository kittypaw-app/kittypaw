.PHONY: help list contracts-check smoke-local e2e-local full-local-live

help:
	@echo "Targets:"
	@echo "  list             List skeleton files"
	@echo "  contracts-check  Validate JSON contract files with jq"
	@echo "  smoke-local      Run repeatable local cross-service smoke"
	@echo "  e2e-local        Run Docker-backed local auth/space E2E"
	@echo "  full-local-live  Run smoke, Docker E2E, and live public-data integrations"

list:
	@find . -maxdepth 5 -type f | sort

contracts-check:
	@find contracts -name '*.json' -print0 | xargs -0 -n1 jq empty

smoke-local:
	@scripts/smoke-local.sh

e2e-local:
	@scripts/e2e-local.sh

full-local-live:
	@scripts/smoke-local.sh
	@scripts/e2e-local.sh
	@set -e; \
		trap 'make -C apps/kittyapi test-integration-down >/dev/null' EXIT; \
		make -C apps/kittyapi test-integration; \
		scripts/with-kittyapi-public-env.sh -- make -C apps/kittyapi test-integration-public
