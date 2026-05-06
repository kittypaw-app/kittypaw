#!/bin/sh
set -e

REPO="${KITTYPAW_INSTALL_REPO:-kittypaw-app/kitty}"
SCRIPT_URL="${KITTYPAW_INSTALL_SCRIPT_URL:-https://raw.githubusercontent.com/${REPO}/main/apps/kittypaw/install-kittypaw.sh}"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL --proto '=https' --tlsv1.2 "$SCRIPT_URL" | sh -s -- "$@"
elif command -v wget >/dev/null 2>&1; then
  wget -qO- "$SCRIPT_URL" | sh -s -- "$@"
else
  echo "Error: install requires curl or wget." >&2
  exit 1
fi
