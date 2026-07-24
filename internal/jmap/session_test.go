// SPDX-License-Identifier: Apache-2.0

package jmap

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWellKnownJMAPURL(t *testing.T) {
	got, err := WellKnownJMAPURL("https://mail.example.com/jmap/")
	if err != nil {
		t.Fatalf("WellKnownJMAPURL() error: %v", err)
	}
	want := "https://mail.example.com/.well-known/jmap"
	if got != want {
		t.Errorf("WellKnownJMAPURL() = %q, want %q", got, want)
	}
}

func TestWellKnownJMAPURL_StripsQueryAndFragment(t *testing.T) {
	got, err := WellKnownJMAPURL("https://mail.example.com/jmap/?x=1#frag")
	if err != nil {
		t.Fatalf("WellKnownJMAPURL() error: %v", err)
	}
	want := "https://mail.example.com/.well-known/jmap"
	if got != want {
		t.Errorf("WellKnownJMAPURL() = %q, want %q", got, want)
	}
}

func TestWellKnownJMAPURL_RelativeInvalid(t *testing.T) {
	if _, err := WellKnownJMAPURL("/jmap/"); err == nil {
		t.Fatal("expected error for relative URL")
	}
}

func TestClientRewriteAdvertisedURL(t *testing.T) {
	client := NewClient("https://jmap.example.com/jmap/", "token", "acc1")
	if err := client.SetAdvertisedHostOverride("jmap.example.com"); err != nil {
		t.Fatalf("SetAdvertisedHostOverride() error: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "https upload",
			in:   "https://mail.example.net:8443/jmap/upload/acc1/?type=sieve",
			want: "https://jmap.example.com:8443/jmap/upload/acc1/?type=sieve",
		},
		{
			name: "websocket",
			in:   "wss://mail.example.net/jmap/ws",
			want: "wss://jmap.example.com/jmap/ws",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.RewriteAdvertisedURL(tt.in)
			if err != nil {
				t.Fatalf("RewriteAdvertisedURL() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("RewriteAdvertisedURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientRewriteAdvertisedURLDisabledPreservesInput(t *testing.T) {
	client := NewClient("https://mail.example.com/jmap/", "token", "acc1")
	rawURL := ":// deliberately not parsed"
	got, err := client.RewriteAdvertisedURL(rawURL)
	if err != nil {
		t.Fatalf("RewriteAdvertisedURL() error: %v", err)
	}
	if got != rawURL {
		t.Errorf("RewriteAdvertisedURL() = %q, want unchanged %q", got, rawURL)
	}
}

func TestClientSetAdvertisedHostOverrideValidation(t *testing.T) {
	tests := []struct {
		host    string
		wantErr bool
	}{
		{host: "jmap.example.com"},
		{host: "127.0.0.1"},
		{host: "[2001:db8::1]"},
		{host: "https://jmap.example.com", wantErr: true},
		{host: "jmap.example.com:443", wantErr: true},
		{host: "user@jmap.example.com", wantErr: true},
		{host: "jmap.example.com/path", wantErr: true},
		{host: "jmáp.example", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			client := NewClient("https://mail.example.com/jmap/", "token", "acc1")
			err := client.SetAdvertisedHostOverride(tt.host)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientRewriteAdvertisedURLIPv6PreservesPort(t *testing.T) {
	client := NewClient("https://mail.example.com/jmap/", "token", "acc1")
	if err := client.SetAdvertisedHostOverride("2001:db8::1"); err != nil {
		t.Fatalf("SetAdvertisedHostOverride() error: %v", err)
	}
	got, err := client.RewriteAdvertisedURL("wss://mail.example.com:8443/jmap/ws")
	if err != nil {
		t.Fatalf("RewriteAdvertisedURL() error: %v", err)
	}
	if want := "wss://[2001:db8::1]:8443/jmap/ws"; got != want {
		t.Errorf("RewriteAdvertisedURL() = %q, want %q", got, want)
	}
}

func TestClient_DiscoverSession(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jmap" {
			t.Errorf("path = %q, want /.well-known/jmap", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("Authorization = %q, want bearer token", auth)
		}
		writeJSON(w, map[string]interface{}{
			"uploadUrl": "https://mail.example.com/upload/{accountId}/",
			"capabilities": map[string]interface{}{
				CapWebSocket: map[string]interface{}{
					"url":          "wss://mail.example.com/jmap/ws",
					"supportsPush": true,
				},
			},
			"primaryAccounts": map[string]string{
				CapMail: "acc1",
			},
		})
	})
	defer closeServer()

	client := newTestClient(url + "/jmap/")
	session, err := client.DiscoverSession()
	if err != nil {
		t.Fatalf("DiscoverSession() error: %v", err)
	}
	if session.MailPrimaryAccountID() != "acc1" {
		t.Errorf("MailPrimaryAccountID() = %q, want acc1", session.MailPrimaryAccountID())
	}
	if session.UploadURL != "https://mail.example.com/upload/{accountId}/" {
		t.Errorf("UploadURL = %q", session.UploadURL)
	}
	capability, ok, err := session.WebSocketCapability()
	if err != nil {
		t.Fatalf("WebSocketCapability() error: %v", err)
	}
	if !ok || capability == nil {
		t.Fatal("websocket capability missing")
	}
	if capability.URL != "wss://mail.example.com/jmap/ws" {
		t.Errorf("capability.URL = %q", capability.URL)
	}
	if !capability.SupportsPush {
		t.Error("SupportsPush = false, want true")
	}
}

func TestClient_DiscoverSession_FollowsSameOriginRedirect(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
				t.Errorf("Authorization = %q, want bearer token", auth)
			}
			writeJSON(w, map[string]interface{}{
				"capabilities": map[string]interface{}{
					CapWebSocket: map[string]interface{}{
						"url":          "wss://mail.example.com/jmap/ws",
						"supportsPush": true,
					},
				},
				"primaryAccounts": map[string]string{CapMail: "acc1"},
			})
			return
		}
		http.Redirect(w, r, "/session", http.StatusTemporaryRedirect)
	})
	defer closeServer()

	client := newTestClient(url + "/jmap/")
	if _, err := client.DiscoverSession(); err != nil {
		t.Fatalf("DiscoverSession() error: %v", err)
	}
}

func TestClient_DiscoverSession_DoesNotFollowCrossOriginRedirect(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.example/.well-known/jmap", http.StatusTemporaryRedirect)
	})
	defer closeServer()

	client := newTestClient(url + "/jmap/")
	if _, err := client.DiscoverSession(); err == nil {
		t.Fatal("expected cross-origin redirect response to fail discovery")
	}
}

func TestClient_DiscoverSession_LimitsSameOriginRedirects(t *testing.T) {
	redirects := 0
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		redirects++
		http.Redirect(w, r, r.URL.Path, http.StatusTemporaryRedirect)
	})
	defer closeServer()

	client := newTestClient(url + "/jmap/")
	if _, err := client.DiscoverSession(); err == nil {
		t.Fatal("expected redirect loop to fail discovery")
	}
	if redirects > 11 {
		t.Fatalf("followed too many redirects: %d", redirects)
	}
}

func TestSession_WebSocketCapabilityUnsupported(t *testing.T) {
	session := &Session{Capabilities: map[string]json.RawMessage{}}
	capability, ok, err := session.WebSocketCapability()
	if err != nil {
		t.Fatalf("WebSocketCapability() error: %v", err)
	}
	if ok || capability != nil {
		t.Fatalf("WebSocketCapability() = (%+v, %v), want no capability", capability, ok)
	}
}

func TestSession_WebSocketCapabilitySupportsFalse(t *testing.T) {
	session := &Session{
		Capabilities: map[string]json.RawMessage{
			CapWebSocket: []byte(`{"url":"wss://mail.example.com/jmap/ws","supportsPush":false}`),
		},
	}
	capability, ok, err := session.WebSocketCapability()
	if err != nil {
		t.Fatalf("WebSocketCapability() error: %v", err)
	}
	if !ok || capability == nil {
		t.Fatal("expected websocket capability")
	}
	if capability.SupportsPush {
		t.Error("SupportsPush = true, want false")
	}
}

func TestResolveUploadURL(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		accountID string
		want      string
		wantErr   bool
	}{
		{
			name:      "stalwart template",
			template:  "https://mail.example.com/upload/{accountId}/",
			accountID: "acc1",
			want:      "https://mail.example.com/upload/acc1/",
		},
		{
			name:      "reserved account chars in path",
			template:  "https://mail.example.com/upload/{accountId}/",
			accountID: "user+tag@example.com 100%/inbox",
			want:      "https://mail.example.com/upload/user%2Btag%40example.com%20100%25%2Finbox/",
		},
		{
			name:      "query position",
			template:  "https://mail.example.com/upload?account={accountId}&kind=sieve",
			accountID: "user+tag@example.com 100%/inbox",
			want:      "https://mail.example.com/upload?account=user%2Btag%40example.com%20100%25%2Finbox&kind=sieve",
		},
		{
			name:      "missing placeholder",
			template:  "https://mail.example.com/upload/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "leftover unsupported variable",
			template:  "https://mail.example.com/upload/{accountId}/{blobId}",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "relative",
			template:  "/upload/{accountId}/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "no host",
			template:  "https:///upload/{accountId}/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "fragment",
			template:  "https://mail.example.com/upload/{accountId}/#frag",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "userinfo",
			template:  "https://user@mail.example.com/upload/{accountId}/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "unsupported scheme",
			template:  "ftp://mail.example.com/upload/{accountId}/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "http localhost allowed",
			template:  "http://localhost/upload/{accountId}/",
			accountID: "acc1",
			want:      "http://localhost/upload/acc1/",
		},
		{
			name:      "http remote rejected",
			template:  "http://mail.example.com/upload/{accountId}/",
			accountID: "acc1",
			wantErr:   true,
		},
		{
			name:      "empty account",
			template:  "https://mail.example.com/upload/{accountId}/",
			accountID: "",
			wantErr:   true,
		},
		{
			name:      "empty template",
			template:  "",
			accountID: "acc1",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveUploadURL(tt.template, tt.accountID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveUploadURL() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveUploadURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
