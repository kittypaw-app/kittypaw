"""
KittySpace deployment - fab setup / fab deploy / fab smoke / fab logs / fab status / fab rollback
"""
import json
import os
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

from fabric import task

HOST = os.environ.get("DEPLOY_HOST") or "second"
DOMAIN = os.environ.get("DEPLOY_DOMAIN") or "space.kittypaw.app"
REMOTE_DIR = os.environ.get("DEPLOY_REMOTE_DIR") or "/home/jinto/kittyspace"
SERVICE_USER = os.environ.get("DEPLOY_USER") or "jinto"
SERVICE_GROUP = os.environ.get("DEPLOY_GROUP") or SERVICE_USER
SERVICE = "kittyspace"
BINARY = "kittyspace"

LOCAL_ROOT = Path(__file__).resolve().parent
REPO_ROOT = LOCAL_ROOT.parent.parent


def _conn():
    from fabric import Connection

    return Connection(HOST)


def _local_build():
    """Cross-compile for Linux x86_64 (static binary, no CGO)."""
    version, commit = _build_metadata()
    print(f"Building {BINARY} for linux/amd64 ({version} {commit}) ...")
    env = {**os.environ, "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0"}
    result = subprocess.run(
        [
            "go",
            "build",
            "-ldflags",
            f"-s -w -X main.version={version} -X main.commit={commit}",
            "-o",
            f"{BINARY}-linux",
            "./cmd/kittyspace",
        ],
        cwd=LOCAL_ROOT,
        env=env,
    )
    if result.returncode != 0:
        print("Build failed.")
        sys.exit(1)
    return LOCAL_ROOT / f"{BINARY}-linux"


def _git(*args, default="unknown"):
    try:
        return subprocess.check_output(["git", *args], cwd=REPO_ROOT, text=True).strip()
    except Exception:
        return default


def _build_metadata():
    version = os.environ.get("VERSION") or _git("describe", "--tags", "--always", default="dev")
    commit = os.environ.get("COMMIT") or _git("rev-parse", "--short=12", "HEAD")
    return version, commit


def _kittypaw_tag(version, tag=None):
    if tag:
        return tag
    if version.startswith("kittypaw/v"):
        return version
    if version.startswith("v"):
        return f"kittypaw/{version}"
    return f"kittypaw/v{version}"


def _kittypaw_stable_payload(version, commit=None, tag=None):
    resolved_tag = _kittypaw_tag(version, tag)
    resolved_commit = commit or _git("rev-list", "-n", "1", resolved_tag, default="")
    if not resolved_commit:
        raise SystemExit(f"Could not resolve commit for {resolved_tag}; pass --commit explicitly.")
    return {
        "channel": "stable",
        "version": resolved_tag.removeprefix("kittypaw/v"),
        "tag": resolved_tag,
        "commit": resolved_commit,
        "promoted_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    }


def _remote_binary_path(suffix=""):
    return f"{REMOTE_DIR}/{BINARY}{suffix}"


def _render_template(path):
    rendered = path.read_text(encoding="utf-8")
    replacements = {
        "{{DOMAIN}}": DOMAIN,
        "{{REMOTE_DIR}}": REMOTE_DIR,
        "{{SERVICE_USER}}": SERVICE_USER,
        "{{SERVICE_GROUP}}": SERVICE_GROUP,
    }
    for old, new in replacements.items():
        rendered = rendered.replace(old, new)
    return rendered


def _put_rendered(c, source, remote_path):
    rendered = _render_template(source)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as tmp:
        tmp.write(rendered)
        tmp_path = tmp.name
    try:
        c.put(tmp_path, remote_path)
    finally:
        Path(tmp_path).unlink(missing_ok=True)


@task
def setup(ctx):
    """Initial server setup: directories, nginx, systemd, env template."""
    c = _conn()

    c.run(f"mkdir -p {REMOTE_DIR}")
    _put_rendered(c, LOCAL_ROOT / "deploy" / "kittyspace.service", "/tmp/kittyspace.service")
    _put_rendered(c, LOCAL_ROOT / "deploy" / "kittyspace.nginx", "/tmp/kittyspace.nginx")

    c.sudo("cp /tmp/kittyspace.service /etc/systemd/system/kittyspace.service")
    c.sudo("cp /tmp/kittyspace.nginx /etc/nginx/sites-enabled/kittyspace")
    c.sudo("systemctl daemon-reload")
    c.sudo("systemctl enable kittyspace")
    c.sudo("nginx -t")
    c.sudo("systemctl reload nginx")

    exists = c.run(f"test -f {REMOTE_DIR}/.env", warn=True)
    if not exists.ok:
        c.put(str(LOCAL_ROOT / "deploy" / "env.example"), f"{REMOTE_DIR}/.env")
        print(f"\n>>> .env created from template at {REMOTE_DIR}/.env; review it before deploy.")


@task
def deploy(ctx):
    """Build, upload binary, restart service, then run prod smoke."""
    binary_path = _local_build()
    c = _conn()

    c.run(f"cp {_remote_binary_path()} {_remote_binary_path('.prev')} 2>/dev/null || true")
    c.put(str(binary_path), _remote_binary_path(".new"))
    c.run(f"chmod +x {_remote_binary_path('.new')}")
    c.run(f"mv {_remote_binary_path('.new')} {_remote_binary_path()}")

    c.sudo(f"systemctl restart {SERVICE}")
    c.run("sleep 2")
    c.sudo(f"systemctl is-active {SERVICE}")
    print("Deployed.")

    smoke(ctx)


@task
def smoke(ctx):
    """Run prod smoke against space.kittypaw.app or BASE_URL override."""
    env = {**os.environ}
    if not env.get("BASE_URL"):
        env["BASE_URL"] = f"https://{DOMAIN}"
    result = subprocess.run(
        ["bash", str(LOCAL_ROOT / "deploy" / "smoke.sh")],
        cwd=LOCAL_ROOT,
        env=env,
    )
    if result.returncode != 0:
        print("Smoke failed; see above for the failing endpoint.")
        sys.exit(result.returncode)


@task
def promote_kittypaw_stable(ctx, version, commit="", tag=""):
    """Publish kittypaw stable metadata without committing stable.json."""
    payload = _kittypaw_stable_payload(version, commit=commit or None, tag=tag or None)
    rendered = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    remote_dir = f"{REMOTE_DIR}/public/kittypaw"
    remote_path = f"{remote_dir}/stable.json"
    c = _conn()
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as tmp:
        tmp.write(rendered)
        tmp_path = tmp.name
    try:
        c.run(f"mkdir -p {remote_dir}")
        c.put(tmp_path, remote_path + ".new")
        c.run(f"cp {remote_path} {remote_path}.prev 2>/dev/null || true")
        c.run(f"mv {remote_path}.new {remote_path}")
    finally:
        Path(tmp_path).unlink(missing_ok=True)

    url = f"https://{DOMAIN}/downloads/kittypaw/stable.json"
    print(f"Published {payload['tag']} to {url}")


@task
def rollback(ctx):
    """Restore previous binary and restart."""
    c = _conn()
    c.run(f"cp {_remote_binary_path('.prev')} {_remote_binary_path()}")
    c.sudo(f"systemctl restart {SERVICE}")
    c.sudo(f"systemctl is-active {SERVICE}")
    print("Rolled back.")


@task
def status(ctx):
    """Show service status."""
    c = _conn()
    c.sudo(f"systemctl status {SERVICE} --no-pager", warn=True)


@task
def logs(ctx, lines=100):
    """Show recent service logs."""
    c = _conn()
    c.sudo(f"journalctl -u {SERVICE} -n {lines} --no-pager")
