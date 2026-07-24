// SPDX-License-Identifier: Apache-2.0

package jmap

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const CapWebSocket = "urn:ietf:params:jmap:websocket"

type Session struct {
	Capabilities    map[string]json.RawMessage `json:"capabilities"`
	PrimaryAccounts map[string]string          `json:"primaryAccounts"`
	UploadURL       string                     `json:"uploadUrl"`
}

type WebSocketCapability struct {
	URL          string `json:"url"`
	SupportsPush bool   `json:"supportsPush"`
}

func (s *Session) WebSocketCapability() (*WebSocketCapability, bool, error) {
	if s == nil || s.Capabilities == nil {
		return nil, false, nil
	}
	raw, ok := s.Capabilities[CapWebSocket]
	if !ok {
		return nil, false, nil
	}
	var capability WebSocketCapability
	if err := json.Unmarshal(raw, &capability); err != nil {
		return nil, true, fmt.Errorf("parse websocket capability: %w", err)
	}
	return &capability, true, nil
}

func (s *Session) MailPrimaryAccountID() string {
	if s == nil || s.PrimaryAccounts == nil {
		return ""
	}
	return s.PrimaryAccounts[CapMail]
}

func (c *Client) DiscoverSession() (*Session, error) {
	endpoint, err := WellKnownJMAPURL(c.URL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create session request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	httpClient := *c.http
	httpClient.CheckRedirect = sameOriginRedirectOnly

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("session request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read session response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &AuthError{StatusCode: resp.StatusCode, Body: string(body)}
		}
		return nil, fmt.Errorf("session discovery returned status %d: %s", resp.StatusCode, string(body))
	}

	var session Session
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, fmt.Errorf("unmarshal session response: %w", err)
	}
	return &session, nil
}

func sameOriginRedirectOnly(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	origin := via[0].URL
	if req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host {
		return http.ErrUseLastResponse
	}
	return nil
}

func WellKnownJMAPURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse jmap url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("jmap url must be absolute")
	}
	parsed.Path = "/.well-known/jmap"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func ResolveUploadURL(template, accountID string) (string, error) {
	if template == "" {
		return "", fmt.Errorf("uploadUrl template is empty")
	}
	if accountID == "" {
		return "", fmt.Errorf("accountId is empty")
	}
	if !strings.Contains(template, "{accountId}") {
		return "", fmt.Errorf("uploadUrl template missing accountId variable")
	}

	expanded := strings.ReplaceAll(template, "{accountId}", percentEncodeURIComponent(accountID))
	if strings.Contains(expanded, "{") || strings.Contains(expanded, "}") {
		return "", fmt.Errorf("uploadUrl template contains unsupported variables")
	}

	if err := validateUploadURL(expanded); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(expanded)
	return parsed.String(), nil
}

func validateUploadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse uploadUrl: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("uploadUrl must be absolute")
	}
	if parsed.Fragment != "" || strings.Contains(rawURL, "#") {
		return fmt.Errorf("uploadUrl must not contain a fragment")
	}
	if parsed.User != nil {
		return fmt.Errorf("uploadUrl must not contain userinfo")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLocalhost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("uploadUrl must use https")
	default:
		return fmt.Errorf("uploadUrl has unsupported scheme")
	}
}

func normalizeAdvertisedHostname(host string) (string, error) {
	if host == "" {
		return "", nil
	}
	if strings.TrimSpace(host) != host {
		return "", fmt.Errorf("advertised host override must not contain whitespace")
	}

	ipHost := host
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		ipHost = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(ipHost); ip != nil {
		return ipHost, nil
	}
	if strings.ContainsAny(host, ":/?#@\\%[]") {
		return "", fmt.Errorf("advertised host override must be a hostname without a scheme, port, or path")
	}
	if len(host) > 253 {
		return "", fmt.Errorf("advertised host override is too long")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("advertised host override is not a valid hostname")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("advertised host override is not a valid ASCII hostname")
			}
		}
	}
	return host, nil
}

func rewriteURLHostname(rawURL, host string) (string, error) {
	if host == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse advertised URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "", fmt.Errorf("advertised URL must be absolute")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("advertised URL must not contain userinfo")
	}
	if parsed.Fragment != "" || strings.Contains(rawURL, "#") {
		return "", fmt.Errorf("advertised URL must not contain a fragment")
	}
	port := parsed.Port()
	if strings.Contains(host, ":") {
		if port == "" {
			parsed.Host = "[" + host + "]"
		} else {
			parsed.Host = net.JoinHostPort(host, port)
		}
	} else if port == "" {
		parsed.Host = host
	} else {
		parsed.Host = net.JoinHostPort(host, port)
	}
	return parsed.String(), nil
}

func percentEncodeURIComponent(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isUnreservedURIByte(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isUnreservedURIByte(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}
