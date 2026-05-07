#!/bin/sh
# Register the KittyPaw server as a per-user LaunchAgent.
#
# launchd does not expand "~" or "$HOME" inside plist values, so this script
# substitutes absolute-path tokens in the template before copying it to
# ~/Library/LaunchAgents/dev.kittypaw.daemon.plist and bootstrapping it with
# launchctl.
#
# Usage:
#   sh packaging/macos/register-service.sh                # default port 3000
#   KITTYPAW_BIND_PORT=3001 sh packaging/macos/register-service.sh
#   KITTYPAW_BIN=/custom/path sh packaging/macos/register-service.sh

set -eu

TEMPLATE="$(cd "$(dirname "$0")" && pwd)/LaunchAgents/dev.kittypaw.daemon.plist"
LABEL="dev.kittypaw.daemon"
DEST_DIR="$HOME/Library/LaunchAgents"
DEST_PLIST="$DEST_DIR/${LABEL}.plist"
KITTYPAW_BIN="${KITTYPAW_BIN:-$(command -v kittypaw 2>/dev/null || echo /usr/local/bin/kittypaw)}"
KITTYPAW_BIND_PORT="${KITTYPAW_BIND_PORT:-3000}"

if [ ! -f "$TEMPLATE" ]; then
  echo "error: plist template not found: $TEMPLATE" >&2
  exit 1
fi

if [ ! -x "$KITTYPAW_BIN" ]; then
  echo "warning: kittypaw binary not found or not executable at: $KITTYPAW_BIN" >&2
  echo "         set KITTYPAW_BIN=... or install the binary first." >&2
fi

DOMAIN="gui/$(id -u)"

# --- Port conflict detection -------------------------------------------------
# Bootout our own service (if loaded) before checking — a repeat install
# shouldn't be blocked by the previous run's listener.
if launchctl print "${DOMAIN}/${LABEL}" >/dev/null 2>&1; then
  launchctl bootout "${DOMAIN}/${LABEL}" 2>/dev/null || true
fi

port_in_use() {
  _port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"${_port}" -sTCP:LISTEN >/dev/null 2>&1
  else
    return 1 # can't detect — proceed and let launchd surface any bind failure
  fi
}

if port_in_use "$KITTYPAW_BIND_PORT"; then
  echo "error: 127.0.0.1:${KITTYPAW_BIND_PORT} is already in use." >&2
  echo "" >&2
  echo "  Another process — likely another OS user's kittypaw server — is" >&2
  echo "  bound to this port. Pick a free port and retry:" >&2
  echo "" >&2
  echo "    KITTYPAW_BIND_PORT=3001 sh $(basename "$0")" >&2
  echo "" >&2
  echo "  Then point your client at the same port, e.g.:" >&2
  echo "    kittypaw chat --remote http://127.0.0.1:3001" >&2
  exit 1
fi

# Log directory must exist before launchd opens StandardOut/ErrorPath,
# otherwise launchd silently drops stdout/stderr.
mkdir -p "$HOME/.kittypaw/logs"
chmod 0700 "$HOME/.kittypaw" 2>/dev/null || true

mkdir -p "$DEST_DIR"

# Substitute absolute-path tokens AND port into the template.
sed \
  -e "s|__KITTYPAW_BIN__|${KITTYPAW_BIN}|g" \
  -e "s|__USER_HOME__|${HOME}|g" \
  -e "s|<string>127.0.0.1:3000</string>|<string>127.0.0.1:${KITTYPAW_BIND_PORT}</string>|" \
  "$TEMPLATE" >"$DEST_PLIST"

echo "installed plist: $DEST_PLIST  (bind 127.0.0.1:${KITTYPAW_BIND_PORT})"

launchctl bootstrap "$DOMAIN" "$DEST_PLIST"
launchctl enable "${DOMAIN}/${LABEL}"
launchctl kickstart -k "${DOMAIN}/${LABEL}" >/dev/null 2>&1 || true

echo ""
echo "done."
echo "  status:  launchctl print ${DOMAIN}/${LABEL} | head -30"
echo "  stdout:  tail -f $HOME/.kittypaw/logs/stdout.log"
echo "  stderr:  tail -f $HOME/.kittypaw/logs/stderr.log"
