package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInstallScriptRestartsLoadedMacOSService(t *testing.T) {
	env := installScriptFixture(t, "Darwin", "arm64")
	env.setFake("FAKE_LAUNCHCTL_LOADED", "1")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	log := env.readLog()
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/dev.kittypaw.daemon"
	if !strings.Contains(log, "launchctl print "+target) {
		t.Fatalf("launchctl print was not called for %s\nlog:\n%s", target, log)
	}
	if !strings.Contains(log, "launchctl kickstart -k "+target) {
		t.Fatalf("loaded macOS service was not restarted\nlog:\n%s", log)
	}
}

func TestInstallScriptDoesNotUseLegacyStandaloneDaemonFallback(t *testing.T) {
	env := installScriptFixture(t, "Darwin", "arm64")
	env.setFake("FAKE_KITTYPAW_DAEMON_RUNNING", "1")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	log := env.readLog()
	if strings.Contains(log, "launchctl kickstart") {
		t.Fatalf("standalone daemon path should not restart launchd service\nlog:\n%s", log)
	}
	if strings.Contains(log, "kittypaw daemon") {
		t.Fatalf("installer should not call legacy daemon commands\nlog:\n%s", log)
	}
}

func TestInstallScriptRestartsActiveLinuxService(t *testing.T) {
	env := installScriptFixture(t, "Linux", "x86_64")
	env.setFake("FAKE_SYSTEMD_ACTIVE", "1")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	log := env.readLog()
	if !strings.Contains(log, "systemctl --user is-active --quiet kittypaw.service") {
		t.Fatalf("systemd service status was not checked\nlog:\n%s", log)
	}
	if !strings.Contains(log, "systemctl --user restart kittypaw.service") {
		t.Fatalf("active systemd service was not restarted\nlog:\n%s", log)
	}
}

func TestInstallScriptDefaultsToStableVersion(t *testing.T) {
	env := installScriptFixture(t, "Darwin", "arm64")
	env.unsetFake("VERSION")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Installing kittypaw v9.8.7") {
		t.Fatalf("install output = %q, want stable version 9.8.7", out)
	}
	log := env.readLog()
	if !strings.Contains(log, "https://space.kittypaw.app/downloads/kittypaw/stable.json") {
		t.Fatalf("default installer should fetch hosted stable metadata\nlog:\n%s", log)
	}
	if strings.Contains(log, "releases?per_page=100") {
		t.Fatalf("default installer must not query latest releases\nlog:\n%s", log)
	}
}

func TestInstallScriptVersionOverridesStable(t *testing.T) {
	env := installScriptFixture(t, "Darwin", "arm64")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Installing kittypaw v1.2.3") {
		t.Fatalf("install output = %q, want explicit version 1.2.3", out)
	}
	if log := env.readLog(); strings.Contains(log, "stable.json") {
		t.Fatalf("explicit VERSION must not fetch stable.json\nlog:\n%s", log)
	}
}

func TestInstallScriptLatestChannelUsesLatestRelease(t *testing.T) {
	env := installScriptFixture(t, "Linux", "x86_64")
	env.unsetFake("VERSION")
	env.setFake("KITTYPAW_CHANNEL", "latest")

	out, err := env.runInstallScript()
	if err != nil {
		t.Fatalf("install-kittypaw.sh failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Installing kittypaw v7.6.5") {
		t.Fatalf("install output = %q, want latest release version 7.6.5", out)
	}
	log := env.readLog()
	if !strings.Contains(log, "releases?per_page=100") {
		t.Fatalf("latest channel should query releases\nlog:\n%s", log)
	}
	if strings.Contains(log, "stable.json") {
		t.Fatalf("latest channel must not fetch stable.json\nlog:\n%s", log)
	}
}

func TestInstallScriptStableFetchFailureDoesNotFallbackToLatest(t *testing.T) {
	env := installScriptFixture(t, "Darwin", "arm64")
	env.unsetFake("VERSION")
	env.setFake("FAKE_STABLE_FAIL", "1")

	out, err := env.runInstallScript()
	if err == nil {
		t.Fatalf("install-kittypaw.sh succeeded unexpectedly\n%s", out)
	}
	if !strings.Contains(out, "stable metadata") {
		t.Fatalf("install output = %q, want stable metadata failure", out)
	}
	if log := env.readLog(); strings.Contains(log, "releases?per_page=100") {
		t.Fatalf("stable failure must not fall back to latest releases\nlog:\n%s", log)
	}
}

type installScriptEnv struct {
	t          *testing.T
	root       string
	dir        string
	fakeBin    string
	installDir string
	logPath    string
	env        []string
}

func installScriptFixture(t *testing.T, osName, arch string) *installScriptEnv {
	t.Helper()

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "bin")
	installDir := filepath.Join(dir, "install")
	homeDir := filepath.Join(dir, "home")
	for _, path := range []string{fakeBin, installDir, homeDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	logPath := filepath.Join(dir, "commands.log")
	platformOS, platformArch := installScriptPlatform(osName, arch)
	env := &installScriptEnv{
		t:          t,
		root:       root,
		dir:        dir,
		fakeBin:    fakeBin,
		installDir: installDir,
		logPath:    logPath,
		env: append(os.Environ(),
			"VERSION=1.2.3",
			"INSTALL_DIR="+installDir,
			"HOME="+homeDir,
			"FAKE_LOG="+logPath,
			"FAKE_UNAME_OS="+osName,
			"FAKE_UNAME_ARCH="+arch,
			"FAKE_EXPECTED_PLATFORM="+platformOS+"_"+platformArch,
			"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		),
	}

	env.writeFakeCommand("uname", `#!/bin/sh
case "$1" in
  -s) printf '%s\n' "$FAKE_UNAME_OS" ;;
  -m) printf '%s\n' "$FAKE_UNAME_ARCH" ;;
  *) exit 1 ;;
esac
`)
	env.writeFakeCommand("curl", `#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  elif [ "${1#http}" != "$1" ]; then
    url="$1"
  fi
  shift || true
done
printf 'curl %s out=%s\n' "$url" "$out" >> "$FAKE_LOG"
if [ -z "$out" ]; then
  case "$url" in
    *stable.json)
      [ "$FAKE_STABLE_FAIL" = "1" ] && exit 22
      printf '{"channel":"stable","version":"9.8.7","tag":"kittypaw/v9.8.7","commit":"fake"}\n'
      exit 0
      ;;
    *releases?per_page=100)
      printf '{"tag_name": "kittypaw/v7.6.5"}\n'
      exit 0
      ;;
  esac
  exit 1
fi
case "$out" in
  *checksums.txt) printf 'dummy  %s\n' "kittypaw_${FAKE_EXPECTED_PLATFORM:-darwin_arm64}.tar.gz" > "$out" ;;
  *) printf 'fake tarball\n' > "$out" ;;
esac
`)
	env.writeFakeCommand("shasum", `#!/bin/sh
cat >/dev/null
exit 0
`)
	env.writeFakeCommand("tar", `#!/bin/sh
cat > kittypaw <<'SCRIPT'
#!/bin/sh
printf 'kittypaw %s\n' "$*" >> "$FAKE_LOG"
exit 0
SCRIPT
chmod +x kittypaw
`)
	env.writeFakeCommand("launchctl", `#!/bin/sh
printf 'launchctl %s\n' "$*" >> "$FAKE_LOG"
if [ "$1" = "print" ]; then
  [ "$FAKE_LAUNCHCTL_LOADED" = "1" ] && exit 0
  exit 1
fi
exit 0
`)
	env.writeFakeCommand("systemctl", `#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$FAKE_LOG"
if [ "$1" = "--user" ] && [ "$2" = "is-active" ]; then
  [ "$FAKE_SYSTEMD_ACTIVE" = "1" ] && exit 0
  exit 3
fi
exit 0
`)

	return env
}

func installScriptPlatform(osName, arch string) (string, string) {
	platformOS := strings.ToLower(osName)
	if platformOS == "darwin" {
		platformOS = "darwin"
	}
	if platformOS == "linux" {
		platformOS = "linux"
	}

	platformArch := arch
	if platformArch == "x86_64" {
		platformArch = "amd64"
	}
	if platformArch == "aarch64" {
		platformArch = "arm64"
	}
	return platformOS, platformArch
}

func (e *installScriptEnv) setFake(key, value string) {
	e.env = append(e.env, key+"="+value)
}

func (e *installScriptEnv) unsetFake(key string) {
	e.t.Helper()
	prefix := key + "="
	filtered := e.env[:0]
	for _, entry := range e.env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	e.env = filtered
}

func (e *installScriptEnv) runInstallScript() (string, error) {
	e.t.Helper()
	cmd := exec.Command("/bin/sh", filepath.Join(e.root, "install-kittypaw.sh"))
	cmd.Dir = e.root
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *installScriptEnv) readLog() string {
	e.t.Helper()
	b, err := os.ReadFile(e.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		e.t.Fatalf("read log: %v", err)
	}
	return string(b)
}

func (e *installScriptEnv) writeFakeCommand(name, body string) {
	e.t.Helper()
	path := filepath.Join(e.fakeBin, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		e.t.Fatalf("write fake %s: %v", name, err)
	}
}
