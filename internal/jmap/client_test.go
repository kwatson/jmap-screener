// SPDX-License-Identifier: Apache-2.0

package jmap

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (string, func()) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(handler))
	return s.URL, s.Close
}

func newTestClient(url string) *Client {
	return &Client{
		URL:       url,
		Token:     "test-token",
		AccountID: "acc1",
		http:      &http.Client{},
	}
}

func assertJMAPHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// --- JMAP protocol tests ---

func TestClient_GetMailboxes(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Mailbox/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list": []map[string]interface{}{
						{
							"id":        "mbox1",
							"name":      "Inbox",
							"parentId":  nil,
							"sortOrder": float64(1),
						},
						{
							"id":        "mbox2",
							"name":      "Sent",
							"parentId":  nil,
							"sortOrder": float64(2),
						},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	mailboxes, err := client.GetMailboxes()
	if err != nil {
		t.Fatalf("GetMailboxes failed: %v", err)
	}
	if len(mailboxes) != 2 {
		t.Fatalf("expected 2 mailboxes, got %d", len(mailboxes))
	}
	if mailboxes[0].ID != "mbox1" {
		t.Errorf("mailbox[0].ID = %q, want %q", mailboxes[0].ID, "mbox1")
	}
	if mailboxes[0].Name != "Inbox" {
		t.Errorf("mailbox[0].Name = %q, want %q", mailboxes[0].Name, "Inbox")
	}
	if mailboxes[1].ID != "mbox2" {
		t.Errorf("mailbox[1].ID = %q, want %q", mailboxes[1].ID, "mbox2")
	}
	if mailboxes[1].Name != "Sent" {
		t.Errorf("mailbox[1].Name = %q, want %q", mailboxes[1].Name, "Sent")
	}
}

func TestClient_GetMailboxes_Empty(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Mailbox/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list":      []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	mailboxes, err := client.GetMailboxes()
	if err != nil {
		t.Fatalf("GetMailboxes failed: %v", err)
	}
	if len(mailboxes) != 0 {
		t.Errorf("expected 0 mailboxes, got %d", len(mailboxes))
	}
}

func TestClient_EmailChanges(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/changes", map[string]interface{}{
					"accountId":      "acc1",
					"oldState":       "old",
					"newState":       "new",
					"hasMoreChanges": true,
					"created":        []interface{}{"e1"},
					"updated":        []interface{}{"e2"},
					"destroyed":      []interface{}{"e3"},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	changes, err := client.EmailChanges("old", 500)
	if err != nil {
		t.Fatalf("EmailChanges failed: %v", err)
	}
	if !changes.HasMoreChanges {
		t.Error("HasMoreChanges should be true")
	}
	if len(changes.Changed) != 2 {
		t.Errorf("expected 2 changed, got %d", len(changes.Changed))
	}
	if len(changes.Removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(changes.Removed))
	}
}

func TestClient_EmailChanges_LegacyChangedRemoved(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/changes", map[string]interface{}{
					"accountId":      "acc1",
					"oldState":       "old",
					"newState":       "new",
					"hasMoreChanges": false,
					"changed":        []interface{}{"e1", "e2"},
					"removed":        []interface{}{"e3"},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	changes, err := client.EmailChanges("old", 500)
	if err != nil {
		t.Fatalf("EmailChanges failed: %v", err)
	}
	if len(changes.Changed) != 2 {
		t.Errorf("expected 2 changed, got %d", len(changes.Changed))
	}
	if len(changes.Removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(changes.Removed))
	}
}

func TestClient_EmailChanges_NoMore(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/changes", map[string]interface{}{
					"accountId":      "acc1",
					"oldState":       "s1",
					"newState":       "s2",
					"hasMoreChanges": false,
					"changed":        []interface{}{},
					"removed":        []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	changes, err := client.EmailChanges("s1", 500)
	if err != nil {
		t.Fatalf("EmailChanges failed: %v", err)
	}
	if changes.HasMoreChanges {
		t.Error("HasMoreChanges should be false")
	}
	if changes.NewState != "s2" {
		t.Errorf("NewState = %q, want %q", changes.NewState, "s2")
	}
}

func TestClient_GetEmail(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list": []map[string]interface{}{
						{
							"id": "e1",
							"mailboxIds": map[string]interface{}{
								"mbox1": true,
							},
							"from": []map[string]interface{}{
								{"name": "Alice", "email": "alice@example.com"},
							},
							"sender":     []interface{}{},
							"receivedAt": "2024-01-01T00:00:00Z",
						},
					},
					"notFound": []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	email, err := client.GetEmail("e1")
	if err != nil {
		t.Fatalf("GetEmail failed: %v", err)
	}
	if email == nil {
		t.Fatal("GetEmail returned nil")
	}
	if email.ID != "e1" {
		t.Errorf("ID = %q, want %q", email.ID, "e1")
	}
	if len(email.From) == 0 {
		t.Fatal("From should not be empty")
	}
	if email.From[0].Email != "alice@example.com" {
		t.Errorf("From[0].Email = %q, want %q", email.From[0].Email, "alice@example.com")
	}
}

func TestClient_GetEmails(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list": []map[string]interface{}{
						{
							"id": "e1",
							"from": []map[string]interface{}{
								{"name": "A", "email": "a@example.com"},
							},
						},
						{
							"id": "e2",
							"from": []map[string]interface{}{
								{"name": "B", "email": "b@example.com"},
							},
						},
						{
							"id": "e3",
							"from": []map[string]interface{}{
								{"name": "C", "email": "c@example.com"},
							},
						},
					},
					"notFound": []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	emails, err := client.GetEmails([]string{"e1", "e2", "e3"})
	if err != nil {
		t.Fatalf("GetEmails failed: %v", err)
	}
	if len(emails) != 3 {
		t.Fatalf("expected 3 emails, got %d", len(emails))
	}
	if emails[0].ID != "e1" {
		t.Errorf("emails[0].ID = %q, want %q", emails[0].ID, "e1")
	}
	if emails[1].ID != "e2" {
		t.Errorf("emails[1].ID = %q, want %q", emails[1].ID, "e2")
	}
	if emails[2].ID != "e3" {
		t.Errorf("emails[2].ID = %q, want %q", emails[2].ID, "e3")
	}
}

func TestClient_QueryEmails(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		argsRaw, _ := json.Marshal(req.MethodCalls[0][1])
		var args EmailQueryArgs
		if err := json.Unmarshal(argsRaw, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		if !args.CalculateTotal {
			t.Fatal("calculateTotal = false, want true")
		}
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/query", map[string]interface{}{
					"accountId":  "acc1",
					"queryState": "qs1",
					"ids":        []interface{}{"e1", "e2", "e3"},
					"position":   float64(0),
					"total":      float64(250),
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	ids, total, position, err := client.QueryEmails("mbox_junk", 0, 10)
	if err != nil {
		t.Fatalf("QueryEmails failed: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 ids, got %d", len(ids))
	}
	if total != 250 {
		t.Errorf("total = %d, want 250", total)
	}
	if position != 0 {
		t.Errorf("position = %d, want 0", position)
	}
}

func TestClient_QueryEmails_MissingTotal(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/query", map[string]interface{}{
					"accountId":  "acc1",
					"queryState": "qs1",
					"ids":        []interface{}{"e1"},
					"position":   float64(0),
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	ids, total, _, err := client.QueryEmails("mbox_junk", 0, 10)
	if err != nil {
		t.Fatalf("QueryEmails failed: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("ids len = %d, want 1", len(ids))
	}
	if total != 0 {
		t.Errorf("total = %d, want zero value when server omits total", total)
	}
}

func TestClient_MoveEmail_Success(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/set", map[string]interface{}{
					"accountId": "acc1",
					"updated": map[string]interface{}{
						"e1": map[string]interface{}{},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	setErr, err := client.MoveEmail("e1", "mbox_target")
	if err != nil {
		t.Fatalf("MoveEmail failed: %v", err)
	}
	if setErr != nil {
		t.Errorf("expected nil SetError, got %+v", setErr)
	}
}

func TestClient_MoveEmail_MissingUpdatedAndNotUpdated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/set", map[string]interface{}{
					"accountId": "acc1",
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	if _, err := client.MoveEmail("e1", "mbox_target"); err == nil {
		t.Fatal("expected error for missing updated/notUpdated entry")
	}
}

func TestClient_MoveEmail_NotUpdated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/set", map[string]interface{}{
					"accountId": "acc1",
					"notUpdated": map[string]interface{}{
						"e1": map[string]interface{}{
							"type":        "invalidProperties",
							"description": "Cannot modify this email",
						},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	setErr, err := client.MoveEmail("e1", "mbox_target")
	if err != nil {
		t.Fatalf("MoveEmail returned unexpected error: %v", err)
	}
	if setErr == nil {
		t.Fatal("expected non-nil SetError, got nil")
	}
	if setErr.Type != "invalidProperties" {
		t.Errorf("SetError.Type = %q, want %q", setErr.Type, "invalidProperties")
	}
}

func TestClient_CreateSieveScript(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			if r.Header.Get("Content-Type") != "application/sieve" {
				t.Errorf("upload Content-Type = %q, want application/sieve", r.Header.Get("Content-Type"))
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("upload Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "require [\"fileinto\"];" {
				t.Errorf("upload body = %q", string(body))
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      len(body),
			})
			return
		}
		assertJMAPHeaders(t, r)
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		argsRaw, _ := json.Marshal(req.MethodCalls[0][1])
		var args SieveScriptSetArgs
		if err := json.Unmarshal(argsRaw, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		created, ok := args.Create["script"]
		if !ok {
			t.Fatalf("create keys = %v, want script", args.Create)
		}
		if created.Name != "screener" || created.BlobID != "blob1" {
			t.Fatalf("create input = %+v, want name screener and blob1", created)
		}
		if args.OnSuccessActivateScript != "#script" {
			t.Errorf("onSuccessActivateScript = %q, want #script", args.OnSuccessActivateScript)
		}
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
					"created": map[string]interface{}{
						"script": map[string]interface{}{
							"id": "s1",
						},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	err := client.CreateSieveScript("screener", "require [\"fileinto\"];", true)
	if err != nil {
		t.Fatalf("CreateSieveScript failed: %v", err)
	}
}

func TestClient_UpdateSieveScript(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob2",
				"type":      "application/sieve",
				"size":      21,
			})
			return
		}
		assertJMAPHeaders(t, r)
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		argsRaw, _ := json.Marshal(req.MethodCalls[0][1])
		var args SieveScriptSetArgs
		if err := json.Unmarshal(argsRaw, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		if _, ok := args.Update["server-script-id"]; !ok {
			t.Fatalf("update keys = %v, want server-script-id", args.Update)
		}
		if args.Update["server-script-id"].BlobID != "blob2" {
			t.Errorf("update blobId = %q, want blob2", args.Update["server-script-id"].BlobID)
		}
		if args.OnSuccessActivateScript != "server-script-id" {
			t.Errorf("onSuccessActivateScript = %q, want server-script-id", args.OnSuccessActivateScript)
		}
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
					"updated": map[string]interface{}{
						"server-script-id": map[string]interface{}{},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	err := client.UpdateSieveScript("server-script-id", "require [\"fileinto\"];", true)
	if err != nil {
		t.Fatalf("UpdateSieveScript failed: %v", err)
	}
}

func TestClientUploadURLPrefersDiscoveredTemplate(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {})
	defer closeServer()

	client := newTestClient("https://mail.example.com/jmap")
	if err := client.SetUploadURLTemplate(url + "/custom/{accountId}/upload?type=sieve"); err != nil {
		t.Fatalf("SetUploadURLTemplate() error: %v", err)
	}
	got, err := client.uploadURL()
	if err != nil {
		t.Fatalf("uploadURL() error: %v", err)
	}
	want := url + "/custom/acc1/upload?type=sieve"
	if got != want {
		t.Errorf("uploadURL() = %q, want %q", got, want)
	}
}

func TestClientUploadURLFallsBackToInferredPath(t *testing.T) {
	client := newTestClient("https://mail.example.com/jmap/")
	got, err := client.uploadURL()
	if err != nil {
		t.Fatalf("uploadURL() error: %v", err)
	}
	want := "https://mail.example.com/jmap/upload/acc1/"
	if got != want {
		t.Errorf("uploadURL() = %q, want %q", got, want)
	}
}

func TestClientSetUploadURLTemplateRejectsInvalidAndDoesNotStore(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {})
	defer closeServer()

	client := newTestClient("https://mail.example.com/jmap")
	valid := url + "/custom/{accountId}/"
	if err := client.SetUploadURLTemplate(valid); err != nil {
		t.Fatalf("SetUploadURLTemplate(valid) error: %v", err)
	}
	if err := client.SetUploadURLTemplate("/relative/{accountId}/"); err == nil {
		t.Fatal("expected invalid template error")
	}
	got, err := client.uploadURL()
	if err != nil {
		t.Fatalf("uploadURL() error: %v", err)
	}
	want := url + "/custom/acc1/"
	if got != want {
		t.Errorf("uploadURL() = %q, want previous valid template %q", got, want)
	}
}

func TestClientUploadSieveScriptDoesNotFollowCrossOriginRedirect(t *testing.T) {
	var redirectedHit atomic.Bool
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirected.Close()

	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload/acc1/" {
			t.Fatalf("path = %q, want upload path", r.URL.Path)
		}
		http.Redirect(w, r, redirected.URL+"/steal", http.StatusTemporaryRedirect)
	})
	defer closeServer()

	client := newTestClient(url)
	if _, err := client.uploadSieveScript("require [];"); err == nil {
		t.Fatal("expected upload error for cross-origin redirect")
	}
	if redirectedHit.Load() {
		t.Fatal("cross-origin redirect target was hit")
	}
}

func TestClient_GetSieveScripts(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/get", map[string]interface{}{
					"accountId": "acc1",
					"list": []map[string]interface{}{
						{
							"id":       "s1",
							"name":     "screener",
							"isActive": true,
							"blobId":   "blob1",
						},
					},
					"notFound": []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	scripts, err := client.GetSieveScripts()
	if err != nil {
		t.Fatalf("GetSieveScripts failed: %v", err)
	}
	if len(scripts) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scripts))
	}
	if scripts[0].Name != "screener" {
		t.Errorf("Name = %q, want %q", scripts[0].Name, "screener")
	}
	if !scripts[0].IsActive {
		t.Error("IsActive should be true")
	}
	if scripts[0].BlobID != "blob1" {
		t.Errorf("BlobID = %q, want %q", scripts[0].BlobID, "blob1")
	}
}

// --- Auth header test ---

func TestClient_AuthHeader(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
		}
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Mailbox/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list":      []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err != nil {
		t.Fatalf("GetMailboxes failed: %v", err)
	}
}

// --- Error handling tests ---

func TestClient_AuthError_401(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error is not *AuthError: %T %v", err, err)
	}
	if authErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", authErr.StatusCode)
	}
}

func TestClient_AuthError_403(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Forbidden"))
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error is not *AuthError: %T %v", err, err)
	}
	if authErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", authErr.StatusCode)
	}
}

func TestClient_ServerError_500(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	var authErr *AuthError
	if errors.As(err, &authErr) {
		t.Errorf("500 should NOT be *AuthError, but got: %v", authErr)
	}
}

func TestClient_MalformedJSON(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"foo"`))
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestClient_NoMethodResponses(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			MethodResponses: []MethodResponse{},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected error for empty methodResponses, got nil")
	}
	if !strings.Contains(err.Error(), "no method responses") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_MismatchedMethodResponse(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/get", map[string]interface{}{"accountId": "acc1"}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected method mismatch error")
	}
	if !strings.Contains(err.Error(), "unexpected method response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_MismatchedCallID(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Mailbox/get", map[string]interface{}{"accountId": "acc1", "list": []interface{}{}}, "wrong"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err == nil {
		t.Fatal("expected call id mismatch error")
	}
	if !strings.Contains(err.Error(), "unexpected method response call id") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_MethodError(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			MethodResponses: []MethodResponse{
				{"error", map[string]interface{}{
					"type":        "cannotCalculateChanges",
					"description": "state too old",
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.EmailChanges("old", 500)
	if err == nil {
		t.Fatal("expected method error")
	}
	var methodErr *MethodError
	if !errors.As(err, &methodErr) {
		t.Fatalf("error = %T %v, want *MethodError", err, err)
	}
	if methodErr.Type != "cannotCalculateChanges" {
		t.Errorf("Type = %q", methodErr.Type)
	}
	if !IsInvalidEmailState(err) {
		t.Error("IsInvalidEmailState should be true")
	}
}

func TestClient_InvalidArgs_Defaults(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Capture the request body to verify defaults
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to unmarshal request body: %v", err)
		}
		if len(req.MethodCalls) != 1 {
			t.Fatalf("expected 1 method call, got %d", len(req.MethodCalls))
		}
		// Extract args from method call
		argsRaw, err := json.Marshal(req.MethodCalls[0][1])
		if err != nil {
			t.Fatalf("failed to marshal args: %v", err)
		}
		var args struct {
			Properties []string `json:"properties"`
		}
		json.Unmarshal(argsRaw, &args)

		if len(args.Properties) == 0 {
			t.Error("expected default properties in request, got none")
		}
		hasFrom := false
		hasSender := false
		for _, p := range args.Properties {
			if p == "from" {
				hasFrom = true
			}
			if p == "sender" {
				hasSender = true
			}
		}
		if !hasFrom {
			t.Error("expected 'from' in properties")
		}
		if !hasSender {
			t.Error("expected 'sender' in properties")
		}

		// Return a valid response so the call succeeds
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Email/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list": []map[string]interface{}{
						{
							"id": "e1",
							"from": []map[string]interface{}{
								{"name": "A", "email": "a@example.com"},
							},
						},
					},
					"notFound": []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)

	// Test GetEmail with no properties
	_, err := client.GetEmail("e1")
	if err != nil {
		t.Fatalf("GetEmail failed: %v", err)
	}

	// Test GetEmails with no properties
	_, err = client.GetEmails([]string{"e1"})
	if err != nil {
		t.Fatalf("GetEmails failed: %v", err)
	}
}

// --- Sieve set rejection tests ---

func TestClient_CreateSieveScript_NotCreated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      10,
			})
			return
		}
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
					"notCreated": map[string]interface{}{
						"script": map[string]interface{}{
							"type":        "invalidSieve",
							"description": "Script validation failed",
						},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	err := client.CreateSieveScript("screener", "bad script", true)
	if err == nil {
		t.Fatal("expected error for notCreated, got nil")
	}
	if !strings.Contains(err.Error(), "invalidSieve") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_CreateSieveScript_MissingCreatedAndNotCreated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      10,
			})
			return
		}
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	if err := client.CreateSieveScript("screener", "require []", true); err == nil {
		t.Fatal("expected error for missing created/notCreated entry")
	}
}

func TestClient_UpdateSieveScript_NotUpdated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      10,
			})
			return
		}
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
					"notUpdated": map[string]interface{}{
						"screener": map[string]interface{}{
							"type":        "errorType",
							"description": "error description",
						},
					},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	err := client.UpdateSieveScript("screener", "bad script", true)
	if err == nil {
		t.Fatal("expected error for notUpdated, got nil")
	}
	if !strings.Contains(err.Error(), "screener") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_UpdateSieveScript_MissingUpdatedAndNotUpdated(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload/acc1/" {
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      10,
			})
			return
		}
		assertJMAPHeaders(t, r)
		resp := Response{
			MethodResponses: []MethodResponse{
				{"SieveScript/set", map[string]interface{}{
					"accountId": "acc1",
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	if err := client.UpdateSieveScript("screener", "require []", true); err == nil {
		t.Fatal("expected error for missing updated/notUpdated entry")
	}
}

// --- Concurrency / edge tests ---

func TestClient_NewClient(t *testing.T) {
	client := NewClient("http://example.com/jmap", "mytoken", "myaccount")
	if client.URL != "http://example.com/jmap" {
		t.Errorf("URL = %q, want %q", client.URL, "http://example.com/jmap")
	}
	if client.Token != "mytoken" {
		t.Errorf("Token = %q, want %q", client.Token, "mytoken")
	}
	if client.AccountID != "myaccount" {
		t.Errorf("AccountID = %q, want %q", client.AccountID, "myaccount")
	}
	if client.http == nil {
		t.Error("http client should not be nil")
	}
}

func TestClient_RequestStructure(t *testing.T) {
	url, closeServer := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to unmarshal request: %v", err)
		}

		// Verify using capabilities
		hasCore := false
		hasMail := false
		for _, u := range req.Using {
			if u == CapCore {
				hasCore = true
			}
			if u == CapMail {
				hasMail = true
			}
		}
		if !hasCore {
			t.Error("missing core capability in using")
		}
		if !hasMail {
			t.Error("missing mail capability in using")
		}

		// Verify method calls
		if len(req.MethodCalls) != 1 {
			t.Fatalf("expected 1 method call, got %d", len(req.MethodCalls))
		}

		// Verify method call structure: [methodName, args, callID]
		mc := req.MethodCalls[0]
		if len(mc) != 3 {
			t.Fatalf("expected 3 elements in method call, got %d", len(mc))
		}
		methodName, ok := mc[0].(string)
		if !ok {
			t.Fatalf("methodCall[0] is not string: %T", mc[0])
		}
		if methodName != "Mailbox/get" {
			t.Errorf("methodName = %q, want %q", methodName, "Mailbox/get")
		}

		// Verify args contain accountId
		argsRaw, err := json.Marshal(mc[1])
		if err != nil {
			t.Fatalf("failed to marshal args: %v", err)
		}
		var args struct {
			AccountID string `json:"accountId"`
		}
		json.Unmarshal(argsRaw, &args)
		if args.AccountID != "acc1" {
			t.Errorf("accountId = %q, want %q", args.AccountID, "acc1")
		}

		callID, ok := mc[2].(string)
		if !ok {
			t.Fatalf("methodCall[2] is not string: %T", mc[2])
		}
		if callID != "c1" {
			t.Errorf("callID = %q, want %q", callID, "c1")
		}

		// Return a valid response
		resp := Response{
			MethodResponses: []MethodResponse{
				{"Mailbox/get", map[string]interface{}{
					"accountId": "acc1",
					"state":     "s1",
					"list":      []interface{}{},
				}, "c1"},
			},
		}
		writeJSON(w, resp)
	})
	defer closeServer()

	client := newTestClient(url)
	_, err := client.GetMailboxes()
	if err != nil {
		t.Fatalf("GetMailboxes failed: %v", err)
	}
}
