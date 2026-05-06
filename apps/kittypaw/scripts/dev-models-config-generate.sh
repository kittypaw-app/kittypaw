#!/usr/bin/env bash
# Generate dev-models config block from eval/models.toml.
# Usage: dev-models-config-generate.sh [path/to/models.toml]
# Defaults to apps/kittypaw/eval/models.toml.
# Output (stdout): sentinel header + [llm] default + [[llm.models]] blocks.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec uv run python "$SCRIPT_DIR/dev-models-config-generate.py" "$@"
