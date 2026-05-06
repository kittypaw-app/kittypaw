package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kittypaw-app/kittyapi/internal/cache"
	"github.com/kittypaw-app/kittyapi/internal/proxy"
)

func newTestHandler(upstreamURL string) (*proxy.AirKoreaHandler, *cache.Cache) {
	c := cache.New()
	h := &proxy.AirKoreaHandler{
		Cache:      c,
		HTTPClient: &http.Client{},
		APIKey:     "test-key",
		BaseURL:    upstreamURL,
	}
	return h, c
}

func fakeUpstream(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func failingUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

func TestRealtimeByStation(t *testing.T) {
	upstream := fakeUpstream(`{"response":{"header":{"resultCode":"00"}}}`)
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/realtime/station?stationName=종로구&dataTerm=DAILY", nil)
	w := httptest.NewRecorder()
	h.RealtimeByStation().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRealtimeEndpointsDefaultToVersion13(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*proxy.AirKoreaHandler) http.HandlerFunc
		target  string
	}{
		{
			name:    "station",
			handler: (*proxy.AirKoreaHandler).RealtimeByStation,
			target:  "/v1/air/airkorea/realtime/station?stationName=종로구&dataTerm=DAILY",
		},
		{
			name:    "city",
			handler: (*proxy.AirKoreaHandler).RealtimeByCity,
			target:  "/v1/air/airkorea/realtime/city?sidoName=서울",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotVer string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotVer = r.URL.Query().Get("ver")
				_, _ = w.Write([]byte(`{"response":{"header":{"resultCode":"00"}}}`))
			}))
			defer upstream.Close()

			h, c := newTestHandler(upstream.URL)
			defer c.Close()

			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			w := httptest.NewRecorder()
			tt.handler(h).ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			if gotVer != "1.3" {
				t.Fatalf("upstream ver = %q, want 1.3 for PM2.5 fields", gotVer)
			}
		})
	}
}

func TestRealtimeEndpointsForwardExplicitVersion(t *testing.T) {
	var gotVer string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVer = r.URL.Query().Get("ver")
		_, _ = w.Write([]byte(`{"response":{"header":{"resultCode":"00"}}}`))
	}))
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/realtime/station?stationName=종로구&dataTerm=DAILY&ver=1.2", nil)
	w := httptest.NewRecorder()
	h.RealtimeByStation().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotVer != "1.2" {
		t.Fatalf("upstream ver = %q, want explicit client version", gotVer)
	}
}

func TestRealtimeByStationMissingParams(t *testing.T) {
	h, c := newTestHandler("")
	defer c.Close()

	tests := []struct {
		name string
		url  string
	}{
		{"no params", "/v1/air/airkorea/realtime/station"},
		{"missing dataTerm", "/v1/air/airkorea/realtime/station?stationName=종로구"},
		{"missing stationName", "/v1/air/airkorea/realtime/station?dataTerm=DAILY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()
			h.RealtimeByStation().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestRealtimeByCity(t *testing.T) {
	upstream := fakeUpstream(`{"response":{"header":{"resultCode":"00"}}}`)
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/realtime/city?sidoName=서울", nil)
	w := httptest.NewRecorder()
	h.RealtimeByCity().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRealtimeByCityMissingSido(t *testing.T) {
	h, c := newTestHandler("")
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/realtime/city", nil)
	w := httptest.NewRecorder()
	h.RealtimeByCity().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestForecastNoRequiredParams(t *testing.T) {
	upstream := fakeUpstream(`{"response":{"header":{"resultCode":"00"}}}`)
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/forecast", nil)
	w := httptest.NewRecorder()
	h.Forecast().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCacheHit(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		_, _ = w.Write([]byte(`{"cached":true}`))
	}))
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	handler := h.UnhealthyStations()

	// First request fills cache.
	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/unhealthy", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	upstreamCalled = false

	// Second request should hit cache.
	req = httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/unhealthy", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if upstreamCalled {
		t.Fatal("expected cache hit, but upstream was called")
	}
}

func TestUpstreamFailureWithStaleCache(t *testing.T) {
	c := cache.New()
	defer c.Close()

	// Pre-populate with stale data.
	c.Set("airkorea:/getMinuDustWeekFrcstDspth:returnType=json", []byte(`{"stale":true}`), 1)

	upstream := failingUpstream()
	defer upstream.Close()

	h := &proxy.AirKoreaHandler{
		Cache:      c,
		HTTPClient: &http.Client{},
		APIKey:     "test-key",
		BaseURL:    upstream.URL,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/forecast/weekly", nil)
	w := httptest.NewRecorder()
	h.WeeklyForecast().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (stale), got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Warning") != `110 - "Response is stale"` {
		t.Fatalf("expected Warning header, got %q", w.Header().Get("Warning"))
	}
}

func TestUpstreamFailureNoCache(t *testing.T) {
	upstream := failingUpstream()
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/forecast/weekly", nil)
	w := httptest.NewRecorder()
	h.WeeklyForecast().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestParamsNotLeaked(t *testing.T) {
	var receivedURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	h, c := newTestHandler(upstream.URL)
	defer c.Close()

	// Send unknown param "evil" — should not be forwarded.
	req := httptest.NewRequest(http.MethodGet, "/v1/air/airkorea/realtime/city?sidoName=서울&evil=drop_table", nil)
	w := httptest.NewRecorder()
	h.RealtimeByCity().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if contains(receivedURL, "evil") {
		t.Fatalf("unknown param leaked to upstream: %s", receivedURL)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
