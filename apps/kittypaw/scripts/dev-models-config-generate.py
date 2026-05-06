#!/usr/bin/env python3
import sys
import tomllib
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT = REPO_ROOT / "eval" / "models.toml"

SENTINEL = (
    "# <!-- GENERATED FROM eval/models.toml — do not edit "
    "(run: scripts/dev-models-config-generate.sh) -->"
)
# dev cfg uniform cap. Plan B Iteration 1 measures all 7 models with the same
# cap so latency/quality is comparable; promote to per-model when use cases
# diverge (e.g. "max_tok=256 보조 호출" rejoining from MODEL_GUIDE § 1.1).
DEV_MAX_TOKENS = 1024


def main(argv: list[str]) -> int:
    path = Path(argv[1]) if len(argv) > 1 else DEFAULT
    if not path.is_file():
        print(f"models.toml not found: {path}", file=sys.stderr)
        return 2
    with path.open("rb") as f:
        data = tomllib.load(f)
    models = data.get("model", [])
    if not models:
        print("no [[model]] entries in models.toml", file=sys.stderr)
        return 2

    out = sys.stdout
    out.write(SENTINEL + "\n\n")
    out.write("[llm]\n")
    out.write(f'default = "{models[0]["id"]}"\n')
    for m in models:
        out.write("\n[[llm.models]]\n")
        out.write(f'id = "{m["id"]}"\n')
        out.write(f'provider = "{m["provider"]}"\n')
        out.write(f'model = "{m["model"]}"\n')
        out.write(f"max_tokens = {DEV_MAX_TOKENS}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
