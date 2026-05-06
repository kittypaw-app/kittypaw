#!/bin/sh
set -e

REPO="${KITTYPAW_INSTALL_REPO:-kittypaw-app/kitty}"
BINARY="kittypaw"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CHANNEL="${KITTYPAW_CHANNEL:-stable}"
STABLE_URL="${KITTYPAW_STABLE_URL:-https://raw.githubusercontent.com/${REPO}/main/apps/kittypaw/stable.json}"

restart_after_install() {
  case "$OS" in
    darwin)
      if command -v launchctl >/dev/null 2>&1; then
        TARGET="gui/$(id -u)/dev.kittypaw.daemon"
        if launchctl print "$TARGET" >/dev/null 2>&1; then
          echo "  Restarting running background service..."
          if launchctl kickstart -k "$TARGET" >/dev/null 2>&1; then
            echo "  ✓ background service restarted"
          else
            echo "  ! Installed, but failed to restart the service."
            echo "    Run: launchctl kickstart -k $TARGET"
          fi
          return
        fi
      fi
      ;;
    linux)
      if command -v systemctl >/dev/null 2>&1; then
        if systemctl --user is-active --quiet kittypaw.service >/dev/null 2>&1; then
          echo "  Restarting running background service..."
          if systemctl --user restart kittypaw.service >/dev/null 2>&1; then
            echo "  ✓ background service restarted"
          else
            echo "  ! Installed, but failed to restart the service."
            echo "    Run: systemctl --user restart kittypaw.service"
          fi
          return
        fi
      fi
      ;;
  esac

  :
}

# ----- detect platform -----

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux*)  OS="linux"  ;;
  Darwin*) OS="darwin" ;;
  *)       echo "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# ----- resolve version -----

if [ -z "$VERSION" ]; then
  case "$CHANNEL" in
    stable|"")
      if ! STABLE_JSON="$(curl -fsSL --proto '=https' --tlsv1.2 "$STABLE_URL")"; then
        echo "Error: failed to fetch stable metadata: ${STABLE_URL}" >&2
        echo "Set VERSION=X.Y.Z to install a specific release." >&2
        exit 1
      fi
      STABLE_ONE_LINE="$(printf '%s' "$STABLE_JSON" | tr '\n' ' ')"
      TAG="$(printf '%s\n' "$STABLE_ONE_LINE" | sed -n 's/.*"tag"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
      if [ -z "$TAG" ]; then
        STABLE_VERSION="$(printf '%s\n' "$STABLE_ONE_LINE" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
        case "$STABLE_VERSION" in
          kittypaw/v*) TAG="$STABLE_VERSION" ;;
          v*) TAG="kittypaw/${STABLE_VERSION}" ;;
          ?*) TAG="kittypaw/v${STABLE_VERSION}" ;;
        esac
      fi
      ;;
    latest)
      TAG="$(curl -fsSL --proto '=https' --tlsv1.2 \
        "https://api.github.com/repos/${REPO}/releases?per_page=100" \
        | grep '"tag_name": "kittypaw/v' | head -1 | sed 's/.*"tag_name": "\([^"]*\)".*/\1/')"
      ;;
    *)
      echo "Error: unsupported KITTYPAW_CHANNEL=${CHANNEL} (use stable or latest)." >&2
      exit 1
      ;;
  esac
else
  case "$VERSION" in
    kittypaw/v*) TAG="$VERSION" ;;
    v*) TAG="kittypaw/${VERSION}" ;;
    *) TAG="kittypaw/v${VERSION}" ;;
  esac
fi

VERSION="${TAG#kittypaw/v}"

if [ -z "$VERSION" ]; then
  echo "Failed to determine version" >&2; exit 1
fi

echo "Installing ${BINARY} v${VERSION} (${OS}/${ARCH})..."

# ----- download & verify -----

TARBALL="${BINARY}_${OS}_${ARCH}.tar.gz"
ENC_TAG="$(printf '%s' "$TAG" | sed 's#/#%2F#g')"
BASE_URL="https://github.com/${REPO}/releases/download/${ENC_TAG}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL --proto '=https' --tlsv1.2 "${BASE_URL}/${TARBALL}" -o "${TMPDIR}/${TARBALL}"
curl -fsSL --proto '=https' --tlsv1.2 "${BASE_URL}/checksums.txt" -o "${TMPDIR}/checksums.txt"

# verify checksum
cd "$TMPDIR"
if command -v shasum >/dev/null 2>&1; then
  grep -F "$TARBALL" checksums.txt | shasum -a 256 -c --quiet
elif command -v sha256sum >/dev/null 2>&1; then
  grep -F "$TARBALL" checksums.txt | sha256sum -c --quiet
else
  echo "Error: sha256 verification requires sha256sum or shasum." >&2
  exit 1
fi

# ----- install -----

tar xzf "$TARBALL"

if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY" "$INSTALL_DIR/"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mv "$BINARY" "$INSTALL_DIR/"
fi

case "$INSTALL_DIR" in
  */) INSTALLED_BIN="${INSTALL_DIR}${BINARY}" ;;
  *)  INSTALLED_BIN="${INSTALL_DIR}/${BINARY}" ;;
esac

echo ""
echo "  ✓ ${BINARY} v${VERSION} installed"
restart_after_install
echo ""
echo "  Get started:"
echo "    kittypaw setup"
echo ""
