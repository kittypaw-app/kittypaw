package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestSystemdTemplateIsRenderableAndOwnsRuntimeDirectory(t *testing.T) {
	raw := readDeployFile(t, "kittyhome.service")
	for _, want := range []string{
		"User={{SERVICE_USER}}",
		"Group={{SERVICE_GROUP}}",
		"WorkingDirectory={{REMOTE_DIR}}",
		"EnvironmentFile={{REMOTE_DIR}}/.env",
		"ExecStart={{REMOTE_DIR}}/kittyhome",
		"RuntimeDirectory=kittyhome",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("kittyhome.service missing %q:\n%s", want, raw)
		}
	}
}

func TestNginxTemplateSupportsDomainRenderingAndDaemonWebSockets(t *testing.T) {
	raw := readDeployFile(t, "kittyhome.nginx")
	for _, want := range []string{
		"upstream kittyhome",
		"server_name {{DOMAIN}}",
		"location /daemon/",
		"proxy_set_header Upgrade $http_upgrade",
		`proxy_set_header Connection "upgrade"`,
		"proxy_read_timeout 86400s",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("kittyhome.nginx missing %q:\n%s", want, raw)
		}
	}
}

func readDeployFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}
