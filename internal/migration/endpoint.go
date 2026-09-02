package migration

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseHTTPEndpoint accepts http(s) URLs with a host. Credentials belong in
// dedicated token fields, not in the URL, because endpoints are returned in JSON.
func ParseHTTPEndpoint(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("endpoint must be an http(s) URL")
	}
	if u.User != nil {
		return nil, fmt.Errorf("endpoint must not include credentials")
	}
	return u, nil
}
