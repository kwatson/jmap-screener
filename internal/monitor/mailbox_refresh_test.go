// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kwatson/jmap-screener/internal/config"
	"github.com/kwatson/jmap-screener/internal/jmap"
)

func TestMonitorCycleRefreshesNewTargetAndRetriesFailures(t *testing.T) {
	for _, origin := range []string{"screener", "junk"} {
		for _, failure := range []string{"none", "request", "missing", "resolve"} {
			t.Run(origin+"/"+failure, func(t *testing.T) {
				database := openMonitorTestDB(t)
				resolved := monitorResolvedForWorkflow()
				cfg := &config.Config{Monitor: config.MonitorConfig{ScreenerMailbox: "Screener", JunkMailbox: "Junk"}}
				const sender = "git@cloudpress.com"
				if err := database.UpsertEmailCache("e1", []string{origin}); err != nil {
					t.Fatal(err)
				}
				if err := saveState(database, "before"); err != nil {
					t.Fatal(err)
				}
				mailboxCalls := 0
				var uploaded string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/upload/acc1/" {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						uploaded = string(body)
						_ = json.NewEncoder(w).Encode(map[string]string{"blobId": "blob1"})
						return
					}
					var req jmap.Request
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					method := req.MethodCalls[0][0].(string)
					switch method {
					case "Email/changes":
						args := req.MethodCalls[0][1].(map[string]interface{})
						if args["sinceState"] != "before" {
							t.Errorf("sinceState = %v, want before", args["sinceState"])
						}
						writeMonitorJMAP(w, method, map[string]interface{}{"oldState": "before", "newState": "after", "updated": []string{"e1"}})
					case "Email/get":
						writeMonitorJMAP(w, method, map[string]interface{}{"list": []jmap.Email{monitorEmail("e1", sender, "t")}})
					case "Mailbox/get":
						mailboxCalls++
						if mailboxCalls == 1 && failure == "request" {
							http.Error(w, "unavailable", http.StatusServiceUnavailable)
							return
						}
						mailboxes := []jmap.Mailbox{
							{ID: "screener", Name: "Screener"},
							{ID: "junk", Name: "Junk", Role: stringPtr("junk")},
							{ID: "parent", Name: "Work"},
							{ID: "t", Name: "Cloudpress", ParentID: stringPtr("parent")},
						}
						if mailboxCalls == 1 && failure == "missing" {
							mailboxes = mailboxes[:3]
						}
						if mailboxCalls == 1 && failure == "resolve" {
							mailboxes[3].ParentID = stringPtr("absent")
						}
						writeMonitorJMAP(w, method, map[string]interface{}{"list": mailboxes})
					case "SieveScript/get":
						writeMonitorJMAP(w, method, map[string]interface{}{"list": []map[string]string{{"id": "script1", "name": scriptName}}})
					case "SieveScript/set":
						writeMonitorJMAP(w, method, map[string]interface{}{"updated": map[string]interface{}{"script1": nil}})
					default:
						t.Errorf("unexpected method %s", method)
					}
				}))
				defer server.Close()
				client := jmap.NewClient(server.URL, "token", "acc1")
				var healthy atomic.Bool
				state, err := runMonitorCycle(cfg, client, database, resolved, map[string]bool{}, "before", &healthy)
				if failure != "none" {
					if err == nil || state != "before" {
						t.Fatalf("failed refresh returned state %q, err %v", state, err)
					}
					cached, cacheErr := database.GetEmailCache("e1")
					if cacheErr != nil || cached == nil || !contains(cached.MailboxIDs, origin) {
						t.Fatalf("failed refresh consumed move: cache %+v, err %v", cached, cacheErr)
					}
					stored, stateErr := database.GetState(stateKey)
					if stateErr != nil || stored != "before" {
						t.Fatalf("failed refresh saved state %q, err %v", stored, stateErr)
					}
					assertNoRoute(t, database, sender)
					if _, ok := resolved.Index.ByID["t"]; ok {
						t.Fatal("failed refresh replaced mailbox index")
					}
					state, err = runMonitorCycle(cfg, client, database, resolved, map[string]bool{}, state, &healthy)
				}
				if err != nil || state != "after" {
					t.Fatalf("successful refresh returned state %q, err %v", state, err)
				}
				assertRouteTarget(t, database, sender, "t")
				route, err := database.GetRoute(sender)
				if err != nil || route.TargetMailbox != "Work/Cloudpress" {
					t.Fatalf("route = %+v, err %v", route, err)
				}
				if !strings.Contains(uploaded, `"git@cloudpress.com" { fileinto "Work.Cloudpress"; stop; }`) {
					t.Fatalf("uploaded script missing new route: %s", uploaded)
				}
				if !healthy.Load() {
					t.Fatal("successful cycle should be healthy")
				}
			})
		}
	}
}

func TestRefreshKnownMailboxesDoesNotFetch(t *testing.T) {
	client, closeServer := monitorJMAPServer(t, func(method string, _ map[string]interface{}, w http.ResponseWriter) {
		t.Errorf("unexpected request %s", method)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	defer closeServer()
	if err := refreshUnknownMailboxes(client, monitorResolvedForWorkflow(), []jmap.Email{monitorEmail("e1", "sender@example.com", "target")}, "Screener", "Junk"); err != nil {
		t.Fatal(err)
	}
}
