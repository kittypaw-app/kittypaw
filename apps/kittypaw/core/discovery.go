package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// discoveryBodyLimit caps the JSON we'll read. The payload is ~200 bytes;
// 64 KiB is ~300x headroom and still safe against a gzip-bomb / unbounded
// stream from a malicious or misbehaving server.
const discoveryBodyLimit = 64 * 1024

// DiscoveryResponse is the JSON body returned by GET {apiURL}/discovery.
// The endpoint is unauthenticated and replaces URL fields previously embedded
// in OAuth TokenResponse. Only api_base_url is required; space_base_url,
// chat_relay_url, kakao_relay_url, and skills_registry_url may be empty.
type DiscoveryResponse struct {
	APIBaseURL        string `json:"api_base_url"`
	AuthBaseURL       string `json:"auth_base_url"`
	ConnectBaseURL    string `json:"connect_base_url"`
	SpaceBaseURL      string `json:"space_base_url"`
	ChatRelayURL      string `json:"chat_relay_url"`
	KakaoRelayURL     string `json:"kakao_relay_url"`
	SkillsRegistryURL string `json:"skills_registry_url"`
}

// FetchDiscovery calls GET {base}/discovery and returns the decoded response.
// Returns an error on non-200 status, invalid JSON, or missing api_base_url.
// All returned URLs have trailing slashes trimmed so downstream concatenation
// like url + "/register" remains well-formed.
func FetchDiscovery(base string) (*DiscoveryResponse, error) {
	endpoint := strings.TrimRight(base, "/") + "/discovery"
	client := &http.Client{
		Timeout: 10 * time.Second,
		// Cap redirects so a hostile server can't chain into link-local /
		// metadata addresses during an SSRF pivot. Three hops is plenty.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("discovery request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned %d", resp.StatusCode)
	}

	var d DiscoveryResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, discoveryBodyLimit)).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode discovery response: %w", err)
	}
	d.APIBaseURL = strings.TrimRight(d.APIBaseURL, "/")
	d.AuthBaseURL = strings.TrimRight(d.AuthBaseURL, "/")
	d.ConnectBaseURL = strings.TrimRight(d.ConnectBaseURL, "/")
	d.SpaceBaseURL = strings.TrimRight(d.SpaceBaseURL, "/")
	d.ChatRelayURL = strings.TrimRight(d.ChatRelayURL, "/")
	d.KakaoRelayURL = strings.TrimRight(d.KakaoRelayURL, "/")
	d.SkillsRegistryURL = strings.TrimRight(d.SkillsRegistryURL, "/")
	if d.APIBaseURL == "" {
		return nil, fmt.Errorf("discovery response missing api_base_url")
	}
	return &d, nil
}
