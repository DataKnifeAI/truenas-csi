package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// MinAPIVersion is the minimum TrueNAS versioned API the driver requires.
const MinAPIVersion = "v25.10.0"

const (
	apiPathPrefix     = "/api/"
	apiVersionsPath   = "/api/versions"
	apiVersionCurrent = "current"
)

// resolveAPIURL rewrites rawURL's path to /api/<version>, preserving scheme, host,
// and port. Any existing path (e.g. /api/current) is replaced so operators only
// need to supply the host.
func resolveAPIURL(rawURL, version string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid TrueNAS URL %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid TrueNAS URL %q: missing scheme/host (expected e.g. wss://host)", rawURL)
	}
	u.Path = apiPathPrefix + version
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// fetchSupportedAPIVersions issues GET <http(s)>://<host>/api/versions and returns
// the API versions the server supports (e.g. ["v25.04.0","v25.10.0"]).
func fetchSupportedAPIVersions(ctx context.Context, rawURL string, tlsConfig *tls.Config) ([]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid TrueNAS URL %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid TrueNAS URL %q: missing host", rawURL)
	}

	// The JSON-RPC endpoint uses ws/wss; the versions endpoint is plain HTTP(S)
	// on the same host. Map the scheme accordingly.
	scheme := "https"
	if u.Scheme == "ws" || u.Scheme == "http" {
		scheme = "http"
	}
	versionsURL := (&url.URL{Scheme: scheme, Host: u.Host, Path: apiVersionsPath}).String()

	reqCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, versionsURL, nil)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned HTTP %d", versionsURL, resp.StatusCode)
	}

	var versions []string
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", apiVersionsPath, err)
	}
	return versions, nil
}

// versionSupported reports whether any advertised API version meets minVersion
// (equal or newer). TrueNAS may drop older patch entries from /api/versions
// (e.g. advertise v25.10.1 without v25.10.0); exact-string matching would
// falsely reject those appliances.
func versionSupported(supported []string, minVersion string) bool {
	for _, v := range supported {
		if apiVersionGTE(v, minVersion) {
			return true
		}
	}
	return false
}

// parseAPIVersion parses TrueNAS API versions like "v25.10.0" into [YY, MM, PATCH].
func parseAPIVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// apiVersionGTE reports whether version v is greater than or equal to min.
func apiVersionGTE(v, min string) bool {
	vp, ok1 := parseAPIVersion(v)
	mp, ok2 := parseAPIVersion(min)
	if !ok1 || !ok2 {
		return v == min
	}
	for i := 0; i < 3; i++ {
		if vp[i] != mp[i] {
			return vp[i] > mp[i]
		}
	}
	return true
}

// verifyAndPinAPIVersion rewrites the client's URL to the pinned API version and
// verifies the server advertises it. If the supported-versions list cannot be
// fetched (server temporarily unavailable), it proceeds with the pinned version
// so the reconnect logic can take over instead of crash-looping.
// verifyAndPinAPIVersion points the client at /api/current (the server's native
// API, which avoids TrueNAS's unreliable older-version compat adapter) after
// confirming the server advertises at least MinAPIVersion. A failed preflight is
// non-fatal: it proceeds with /api/current so the reconnect logic can take over.
func (c *Client) verifyAndPinAPIVersion(ctx context.Context) error {
	resolved, err := resolveAPIURL(c.config.URL, apiVersionCurrent)
	if err != nil {
		return err
	}

	supported, ferr := fetchSupportedAPIVersions(ctx, c.config.URL, c.config.TLSConfig)
	if ferr != nil {
		c.log.V(logLevelInfo).Info("Could not fetch supported TrueNAS API versions; proceeding with /api/current (reconnect will retry if the server is unavailable)",
			"error", ferr)
		c.config.URL = resolved
		return nil
	}

	if !versionSupported(supported, MinAPIVersion) {
		return fmt.Errorf("%w: server does not advertise the minimum required version %s (supported: %v); upgrade TrueNAS",
			ErrUnsupportedAPIVersion, MinAPIVersion, supported)
	}

	c.log.V(logLevelInfo).Info("Verified TrueNAS meets the minimum API version; connecting to /api/current",
		"minVersion", MinAPIVersion, "supported", supported)
	c.config.URL = resolved
	return nil
}
