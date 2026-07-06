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
