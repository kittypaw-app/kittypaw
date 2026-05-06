package smoke

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

func TestRunLocalCompletesChatRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out bytes.Buffer
	if err := RunLocal(ctx, &out); err != nil {
		t.Fatalf("RunLocal() error = %v; output=%s", err, out.String())
	}

	output := out.String()
	for _, want := range []string{
		"daemon connected",
		"route discovery",
		"chat completion",
		"bff login",
		"bff chat completion",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want progress containing %q", output, want)
		}
	}
}

func TestRunFakeDaemonDoesNotSignalReadyOnConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ready := make(chan struct{})
	err := runFakeDaemon(ctx, "http://%zz", ready)
	if err == nil {
		t.Fatal("runFakeDaemon() error = nil, want connect error")
	}

	select {
	case <-ready:
		t.Fatal("ready channel was signaled for failed daemon connect")
	default:
	}
}

func TestNewLocalServerReturnsListenError(t *testing.T) {
	listenErr := errors.New("listen blocked")
	srv, err := newLocalServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), func(string, string) (net.Listener, error) {
		return nil, listenErr
	})
	if err == nil {
		t.Fatal("newLocalServer() error = nil, want listen error")
	}
	if !errors.Is(err, listenErr) {
		t.Fatalf("newLocalServer() error = %v, want wrapped listen error", err)
	}
	if srv != nil {
		t.Fatalf("newLocalServer() server = %v, want nil", srv)
	}
}

func TestRunChatCompletionRejectsNonSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"data: [DONE]"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := runChatCompletion(ctx, srv.URL)
	if err == nil {
		t.Fatal("runChatCompletion() error = nil, want content-type error")
	}
	if !strings.Contains(err.Error(), "content-type") {
		t.Fatalf("runChatCompletion() error = %v, want content-type error", err)
	}
}

func TestValidateSmokeRequestRejectsUnexpectedOperation(t *testing.T) {
	err := validateSmokeRequest(protocol.Frame{
		Type:      protocol.FrameRequest,
		AccountID: localAccountID,
		Operation: protocol.OperationOpenAIModels,
		Method:    http.MethodGet,
		Path:      "/v1/models",
		Body:      []byte(`{"messages":[{"role":"user","content":"hello from smoke"}]}`),
	})
	if err == nil {
		t.Fatal("validateSmokeRequest() error = nil, want operation error")
	}
	if !strings.Contains(err.Error(), "operation") {
		t.Fatalf("validateSmokeRequest() error = %v, want operation error", err)
	}
}
