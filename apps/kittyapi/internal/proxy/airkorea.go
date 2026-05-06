package proxy

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kittypaw-app/kittyapi/internal/cache"
)

const (
	airKoreaBaseURL         = "https://apis.data.go.kr/B552584/ArpltnInforInqireSvc"
	airKoreaCacheTTL        = 30 * time.Minute
	maxResponseBody         = 1 << 20 // 1 MB
	airKoreaRealtimeVersion = "1.3"
)

// AirKoreaHandler proxies requests to the AirKorea (에어코리아) public data API.
type AirKoreaHandler struct {
	Cache      *cache.Cache
	HTTPClient *http.Client
	APIKey     string
	BaseURL    string // overridable for testing
}

func (h *AirKoreaHandler) baseURL() string {
	if h.BaseURL != "" {
		return h.BaseURL
	}
	return airKoreaBaseURL
}

// RealtimeByStation proxies 측정소별 실시간 측정정보 조회.
func (h *AirKoreaHandler) RealtimeByStation() http.HandlerFunc {
	return h.endpoint("/getMsrstnAcctoRltmMesureDnsty",
		[]string{"stationName", "dataTerm"},
		[]string{"stationName", "dataTerm", "pageNo", "numOfRows", "ver"},
		airKoreaRealtimeVersion,
	)
}

// RealtimeByCity proxies 시도별 실시간 측정정보 조회.
func (h *AirKoreaHandler) RealtimeByCity() http.HandlerFunc {
	return h.endpoint("/getCtprvnRltmMesureDnsty",
		[]string{"sidoName"},
		[]string{"sidoName", "pageNo", "numOfRows", "ver"},
		airKoreaRealtimeVersion,
	)
}

// Forecast proxies 대기질 예보통보 조회.
func (h *AirKoreaHandler) Forecast() http.HandlerFunc {
	return h.endpoint("/getMinuDustFrcstDspth",
		nil,
		[]string{"searchDate", "informCode", "pageNo", "numOfRows"},
		"",
	)
}

// WeeklyForecast proxies 초미세먼지 주간예보 조회.
func (h *AirKoreaHandler) WeeklyForecast() http.HandlerFunc {
	return h.endpoint("/getMinuDustWeekFrcstDspth",
		nil,
		[]string{"searchDate", "pageNo", "numOfRows"},
		"",
	)
}

// UnhealthyStations proxies 통합대기환경지수 나쁨 이상 측정소 목록조회.
func (h *AirKoreaHandler) UnhealthyStations() http.HandlerFunc {
	return h.endpoint("/getUnityAirEnvrnIdexSnstiveAboveMsrstnList",
		nil,
		[]string{"pageNo", "numOfRows"},
		"",
	)
}

func (h *AirKoreaHandler) endpoint(path string, required, allowed []string, defaultVer string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		for _, p := range required {
			if q.Get(p) == "" {
				http.Error(w, p+" is required", http.StatusBadRequest)
				return
			}
		}

		upstream := url.Values{}
		for _, p := range allowed {
			if v := q.Get(p); v != "" {
				upstream.Set(p, v)
			}
		}
		if defaultVer != "" && upstream.Get("ver") == "" {
			upstream.Set("ver", defaultVer)
		}
		upstream.Set("serviceKey", h.APIKey)
		upstream.Set("returnType", "json")

		key := cacheKey(path, upstream)

		if data, ok := h.Cache.Get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
			return
		}

		data, err := h.fetch(path, upstream)
		if err != nil {
			log.Printf("airkorea upstream error (%s): %v", path, err)
			if stale, isStale, found := h.Cache.GetStale(key); found && isStale {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Warning", `110 - "Response is stale"`)
				_, _ = w.Write(stale)
				return
			}
			http.Error(w, "upstream service unavailable", http.StatusBadGateway)
			return
		}

		h.Cache.Set(key, data, airKoreaCacheTTL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

func (h *AirKoreaHandler) fetch(path string, params url.Values) ([]byte, error) {
	u := h.baseURL() + path + "?" + params.Encode()

	resp, err := h.HTTPClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed", path)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return nil, fmt.Errorf("response %d: %s", resp.StatusCode, body)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
}

func cacheKey(path string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "serviceKey" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("airkorea:")
	b.WriteString(path)
	for _, k := range keys {
		b.WriteByte(':')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params.Get(k))
	}
	return b.String()
}
