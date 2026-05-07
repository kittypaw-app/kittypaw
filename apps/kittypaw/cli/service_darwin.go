//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jinto/kittypaw/packaging"
)

const (
	darwinLabel    = "dev.kittypaw.daemon"
	darwinPlistExt = ".plist"
)

// renderPlist substitutes absolute-path tokens (launchd does not expand
// ~ or $HOME) and the bind port in the LaunchAgent plist template.
func renderPlist(tpl, binPath, bindHost string, bindPort int, home string) string {
	out := strings.ReplaceAll(tpl, "__KITTYPAW_BIN__", binPath)
	out = strings.ReplaceAll(out, "__USER_HOME__", home)
	out = strings.ReplaceAll(out,
		"<string>127.0.0.1:3000</string>",
		fmt.Sprintf("<string>%s:%d</string>", bindHost, bindPort))
	return out
}

func userLaunchAgentPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", darwinLabel+darwinPlistExt)
}

func darwinDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func serviceInstall(stdout, stderr io.Writer, f *serviceFlags) error {
	binPath, err := resolveBinPath(f.binPath)
	if err != nil {
		return err
	}

	// Bootout existing service (if loaded) so the rewrite doesn't race against
	// a live listener and bootstrap doesn't error with "already loaded".
	target := darwinDomain() + "/" + darwinLabel
	if err := run(io.Discard, io.Discard, "launchctl", "print", target); err == nil {
		_ = run(io.Discard, io.Discard, "launchctl", "bootout", target)
	}

	if err := preflightPort(f.bindHost, f.bindPort); err != nil {
		return err
	}

	// launchd does not expand ~ or $HOME — substitute absolute paths.
	plist := renderPlist(packaging.MacOSLaunchAgent, binPath, f.bindHost, f.bindPort, os.Getenv("HOME"))

	// StandardOut/ErrorPath parents must exist before launchd opens them.
	logDir := filepath.Join(os.Getenv("HOME"), ".kittypaw", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", logDir, err)
	}

	destPath := userLaunchAgentPath()
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(destPath), err)
	}
	if err := os.WriteFile(destPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	_, _ = fmt.Fprintf(stdout, "installed plist: %s  (bind %s:%d)\n", destPath, f.bindHost, f.bindPort)

	if err := run(stdout, stderr, "launchctl", "bootstrap", darwinDomain(), destPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	_ = run(stdout, stderr, "launchctl", "enable", target)
	_ = run(stdout, stderr, "launchctl", "kickstart", "-k", target)

	_, _ = fmt.Fprintf(stdout, "\ndone. tail the log with:  %s server logs -f\n", os.Args[0])
	return nil
}

func serviceUninstall(stdout, stderr io.Writer) error {
	target := darwinDomain() + "/" + darwinLabel
	_ = run(stdout, stderr, "launchctl", "bootout", target)
	destPath := userLaunchAgentPath()
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", destPath, err)
	}
	_, _ = fmt.Fprintf(stdout, "removed plist: %s\n", destPath)
	return nil
}

func serviceStatus(stdout, stderr io.Writer) error {
	target := darwinDomain() + "/" + darwinLabel
	if err := run(io.Discard, io.Discard, "launchctl", "print", target); err != nil {
		_, _ = fmt.Fprintln(stdout, "active: no (not loaded)")
		return nil
	}
	// Full `launchctl print` output is verbose — stream only the first 30
	// lines so users see label/pid/program/args without the noise.
	c := exec.Command("sh", "-c", "launchctl print "+target+" | head -30")
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

func serviceLogs(stdout, stderr io.Writer, follow bool) error {
	logPath := filepath.Join(os.Getenv("HOME"), ".kittypaw", "logs", "stderr.log")
	args := []string{}
	if follow {
		args = append(args, "-f")
	} else {
		args = append(args, "-n", "200")
	}
	args = append(args, logPath)
	c := exec.Command("tail", args...)
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}
