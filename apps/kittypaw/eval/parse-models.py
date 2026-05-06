#!/usr/bin/env python3
import json
import sys
import tomllib
from pathlib import Path

DEFAULT = Path(__file__).resolve().parent / "models.toml"


def main(argv: list[str]) -> int:
    path = Path(argv[1]) if len(argv) > 1 else DEFAULT
    if not path.is_file():
        print(f"models.toml not found: {path}", file=sys.stderr)
        return 2
    with path.open("rb") as f:
        data = tomllib.load(f)
    json.dump(data, sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
