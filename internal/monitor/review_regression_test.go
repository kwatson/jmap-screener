// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kwatson/jmap-screener/internal/config"
	"github.com/kwatson/jmap-screener/internal/db"
	"github.com/kwatson/jmap-screener/internal/jmap"
)

func reviewMailboxes() []jmap.Mailbox {
	return []jmap.Mailbox{{ID: "screener", Name: "Screener"}, {ID: "junk", Name: "Junk", Role: stringPtr("junk")}, {ID: "trash", Name: "Trash", Role: stringPtr("trash")}, {ID: "target", Name: "Family"}}
}

func TestRescueSkipsUnsafeCandidates(t *testing.T) {
	for _, scenario := range []string{"junk-target", "stale-membership", "missing-state", "state-mismatch", "not-updated", "missing-email"} {
		t.Run(scenario, func(t *testing.T) {
			database := openMonitorTestDB(t)
			target := "target"
			if scenario == "junk-target" {
				target = "junk"
			}
			if err := database.UpsertRoute("known@example.com", "Family", target); err != nil {
				t.Fatal(err)
			}
			if err := database.UpsertEmailCache("e", []string{"junk"}); err != nil {
				t.Fatal(err)
			}
			queries, sets := 0, 0
			client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
				switch method {
				case "Email/query":
					queries++
					if queries > 1 {
						t.Error("unexpected retry")
						http.Error(w, "unexpected rescue retry", http.StatusInternalServerError)
						return
					}
					writeMonitorJMAP(w, method, map[string]interface{}{"ids": []string{"e"}, "position": 0, "total": 1})
				case "Email/get":
					state := "state1"
					if scenario == "missing-state" {
						state = ""
					}
					mailbox := "junk"
					if scenario == "stale-membership" {
						mailbox = "target"
					}
					list := []jmap.Email{monitorEmail("e", "known@example.com", mailbox)}
					missing := []string{}
					if scenario == "missing-email" {
						list = nil
						missing = []string{"e"}
					}
					writeMonitorJMAP(w, method, map[string]interface{}{"state": state, "list": list, "notFound": missing})
				case "Email/set":
					sets++
					if args["ifInState"] != "state1" {
						t.Errorf("conditional state=%v", args["ifInState"])
					}
					if scenario == "state-mismatch" {
						writeMonitorJMAP(w, "error", map[string]interface{}{"type": "stateMismatch"})
					} else {
						writeMonitorJMAP(w, method, map[string]interface{}{"notUpdated": map[string]interface{}{"e": map[string]string{"type": "forbidden"}}})
					}
				default:
					t.Errorf("unexpected %s", method)
				}
			})
			defer closeServer()
			moved, err := rescueJunkPage(client, database, monitorResolvedForWorkflow(), 1)
			if moved || (err != nil) != (scenario == "missing-state") {
				t.Fatalf("moved=%v err=%v", moved, err)
			}
			wantSets := 0
			if scenario == "state-mismatch" || scenario == "not-updated" {
				wantSets = 1
			}
			if sets != wantSets {
				t.Fatalf("sets=%d want %d", sets, wantSets)
			}
			cache, err := database.GetEmailCache("e")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing-email" {
				if cache != nil {
					t.Fatal("missing email cache retained")
				}
			} else if cache == nil || !contains(cache.MailboxIDs, "junk") {
				t.Fatalf("failed rescue changed cache %+v", cache)
			}
		})
	}
}

func TestRescueSerializesStateAndUserResetThroughCycles(t *testing.T) {
	database := openMonitorTestDB(t)
	for _, sender := range []string{"a@example.com", "b@example.com"} {
		if err := database.UpsertRoute(sender, "Family", "target"); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Monitor: config.MonitorConfig{ScreenerMailbox: "Screener", JunkMailbox: "Junk"}, Rescue: config.RescueConfig{Enabled: true, BatchSize: 10}}
	resolved, err := resolvePersistentMailboxes(database, reviewMailboxes(), "Screener", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	locations := map[string]string{"a": "junk", "b": "junk"}
	version, sets := 0, 0
	changed := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/upload/") {
			writeMonitorJMAPUpload(w)
			return
		}
		var req jmap.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		method := req.MethodCalls[0][0].(string)
		args := req.MethodCalls[0][1].(map[string]interface{})
		switch method {
		case "Mailbox/get":
			writeMonitorJMAP(w, method, map[string]interface{}{"list": reviewMailboxes()})
		case "Email/changes":
			writeMonitorJMAP(w, method, map[string]interface{}{"newState": fmt.Sprint(version), "updated": changed})
			changed = nil
		case "Email/query":
			ids := []string{}
			for _, id := range []string{"a", "b"} {
				if locations[id] == "junk" {
					ids = append(ids, id)
				}
			}
			writeMonitorJMAP(w, method, map[string]interface{}{"ids": ids, "total": len(ids), "position": 0})
		case "Email/get":
			list := []jmap.Email{}
			for _, raw := range args["ids"].([]interface{}) {
				id := raw.(string)
				list = append(list, monitorEmail(id, id+"@example.com", locations[id]))
			}
			writeMonitorJMAP(w, method, map[string]interface{}{"state": fmt.Sprint(version), "list": list})
		case "Email/set":
			if args["ifInState"] != fmt.Sprint(version) {
				t.Errorf("stale state %v want %d", args["ifInState"], version)
			}
			updated := map[string]interface{}{}
			for id := range args["update"].(map[string]interface{}) {
				locations[id] = "target"
				updated[id] = nil
				changed = append(changed, id)
			}
			sets++
			version++
			writeMonitorJMAP(w, method, map[string]interface{}{"updated": updated})
		case "SieveScript/get":
			writeMonitorJMAP(w, method, map[string]interface{}{"list": []interface{}{}})
		case "SieveScript/set":
			writeMonitorJMAP(w, method, map[string]interface{}{"created": map[string]interface{}{"script": map[string]string{"id": "script"}}})
		default:
			t.Errorf("unexpected %s", method)
		}
	}))
	defer server.Close()
	client := jmap.NewClient(server.URL, "token", "acc1")
	var healthy atomic.Bool
	state, err := runMonitorCycle(cfg, client, database, resolved, "initial", &healthy)
	if err != nil {
		t.Fatal(err)
	}
	if sets != 2 {
		t.Fatalf("sets=%d", sets)
	}
	// A user move before the first rescue notification must still reset.
	locations["b"] = "screener"
	// Unchanged self notifications must preserve routes.
	state, err = runMonitorCycle(cfg, client, database, resolved, state, &healthy)
	if err != nil {
		t.Fatal(err)
	}
	assertRouteTarget(t, database, "a@example.com", "target")
	assertNoRoute(t, database, "b@example.com")
	// User can move immediately after rescue, before the next notification.
	locations["a"] = "screener"
	changed = []string{"a"}
	version++
	state, err = runMonitorCycle(cfg, client, database, resolved, state, &healthy)
	if err != nil {
		t.Fatal(err)
	}
	assertNoRoute(t, database, "a@example.com")
	// And choose a replacement route on a later cycle.
	locations["a"] = "target"
	changed = []string{"a"}
	version++
	_, err = runMonitorCycle(cfg, client, database, resolved, state, &healthy)
	if err != nil {
		t.Fatal(err)
	}
	assertRouteTarget(t, database, "a@example.com", "target")
}

func writeMonitorJMAPUpload(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]string{"blobId": "blob1"})
}

func TestMailboxMetadataRefreshUpdatesSieveWithoutEmailChanges(t *testing.T) {
	for _, scenario := range []string{"rename", "screener-rename", "reparent", "delete", "counters", "upload-failure", "refresh-failure"} {
		t.Run(scenario, func(t *testing.T) {
			database := openMonitorTestDB(t)
			resolved, err := resolvePersistentMailboxes(database, reviewMailboxes(), "Screener", "Junk")
			if err != nil {
				t.Fatal(err)
			}
			if err := database.UpsertRoute("a@example.com", "Family", "target"); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Monitor: config.MonitorConfig{ScreenerMailbox: "Screener", JunkMailbox: "Junk"}}
			uploads := []string{}
			failed := false
			changes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/upload/") {
					body, _ := io.ReadAll(r.Body)
					uploads = append(uploads, string(body))
					if scenario == "upload-failure" && !failed {
						failed = true
						http.Error(w, "failed", 503)
						return
					}
					writeMonitorJMAPUpload(w)
					return
				}
				var req jmap.Request
				_ = json.NewDecoder(r.Body).Decode(&req)
				method := req.MethodCalls[0][0].(string)
				switch method {
				case "Mailbox/get":
					if scenario == "refresh-failure" {
						http.Error(w, "failed", 503)
						return
					}
					mailboxes := reviewMailboxes()
					switch scenario {
					case "rename", "upload-failure":
						mailboxes[3].Name = "Gitlab"
					case "screener-rename":
						mailboxes[0].Name = "Screening"
					case "reparent":
						mailboxes[3].ParentID = stringPtr("screener")
					case "delete":
						mailboxes = mailboxes[:3]
					case "counters":
						mailboxes[3].TotalEmails = 42
					}
					writeMonitorJMAP(w, method, map[string]interface{}{"list": mailboxes})
				case "Email/changes":
					changes++
					writeMonitorJMAP(w, method, map[string]interface{}{"newState": "next"})
				case "SieveScript/get":
					writeMonitorJMAP(w, method, map[string]interface{}{"list": []interface{}{}})
				case "SieveScript/set":
					writeMonitorJMAP(w, method, map[string]interface{}{"created": map[string]interface{}{"script": map[string]string{"id": "script"}}})
				default:
					t.Errorf("unexpected %s", method)
				}
			}))
			defer server.Close()
			client := jmap.NewClient(server.URL, "token", "acc1")
			var healthy atomic.Bool
			state, err := runMonitorCycle(cfg, client, database, resolved, "before", &healthy)
			if scenario == "refresh-failure" {
				if err == nil || state != "before" || changes != 0 || resolved.Index.ByID["target"].Name != "Family" {
					t.Fatalf("refresh failure consumed metadata/checkpoint state=%s err=%v", state, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			dirty, _ := database.GetBoolState(sieveDirtyKey)
			if scenario == "upload-failure" {
				if !dirty {
					t.Fatal("failed upload lost dirty flag")
				}
				_, err = runMonitorCycle(cfg, client, database, resolved, state, &healthy)
				if err != nil {
					t.Fatal(err)
				}
				dirty, _ = database.GetBoolState(sieveDirtyKey)
				if len(uploads) != 2 || dirty {
					t.Fatalf("retry uploads=%d dirty=%v", len(uploads), dirty)
				}
			}
			if scenario == "counters" {
				if len(uploads) != 0 {
					t.Fatal("counter update uploaded sieve")
				}
				return
			}
			if len(uploads) == 0 {
				t.Fatal("metadata update did not upload sieve")
			}
			script := uploads[len(uploads)-1]
			switch scenario {
			case "rename", "upload-failure":
				if !strings.Contains(script, `fileinto "Gitlab"`) {
					t.Fatal(script)
				}
			case "reparent":
				if !strings.Contains(script, `fileinto "Screener.Family"`) {
					t.Fatal(script)
				}
			case "screener-rename":
				if !strings.Contains(script, `fileinto "Screening"`) || resolved.Screener.ID != "screener" {
					t.Fatal(script)
				}
			case "delete":
				if strings.Contains(script, "a@example.com") {
					t.Fatal("deleted target included")
				}
			}
		})
	}
}

func TestSpecialMailboxIdentitySurvivesRestartAndSelectorChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	mailboxes := reviewMailboxes()
	mailboxes[1].Role = nil
	if _, err := resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk"); err != nil {
		t.Fatal(err)
	}
	mailboxes[0].Name = "Screening"
	mailboxes[1].Name = "Spam"
	renamed, err := resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk")
	if err != nil || renamed.Junk.ID != "junk" {
		t.Fatalf("configured Junk rename lost identity: %+v %v", renamed, err)
	}
	mailboxes = append(mailboxes, jmap.Mailbox{ID: "replacement", Name: "Screener"}, jmap.Mailbox{ID: "new-junk", Name: "New Junk", Role: stringPtr("junk")})
	resolved, err := resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Screener.ID != "screener" || resolved.Junk.ID != "new-junk" {
		t.Fatalf("identity or role lost: %+v", resolved)
	}
	// Reload from disk exactly as startup does, with no live resolved metadata.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Screener.ID != "screener" {
		t.Fatal("restart retargeted reused name")
	}
	resolved, err = resolvePersistentMailboxes(database, mailboxes, "Family", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Screener.ID != "target" {
		t.Fatal("changed selector ignored")
	}
	mailboxes[4].Role = stringPtr("trash")
	mailboxes[2].Role = nil
	resolved, err = resolvePersistentMailboxes(database, mailboxes, "Family", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Trash.ID != "replacement" {
		t.Fatal("trash role reassignment ignored")
	}
}

// A failed dirty write must not publish new mailbox metadata.
func TestRefreshMailboxPersistenceFailureRetainsIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	writable, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePersistentMailboxes(writable, reviewMailboxes(), "Screener", "Junk")
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := db.Open("file:" + path + "?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	client, closeServer := monitorJMAPServer(t, func(method string, _ map[string]interface{}, w http.ResponseWriter) {
		if method != "Mailbox/get" {
			t.Errorf("unexpected %s", method)
		}
		mailboxes := reviewMailboxes()
		mailboxes[3].Name = "Gitlab"
		writeMonitorJMAP(w, method, map[string]interface{}{"list": mailboxes})
	})
	defer closeServer()
	if err := refreshMailboxes(client, readonly, resolved, "Screener", "Junk", nil); err == nil {
		t.Fatal("readonly refresh succeeded")
	}
	if resolved.Index.ByID["target"].Name != "Family" {
		t.Fatal("unpersisted metadata accepted")
	}
}

func TestRecreatedScreenerReplacesDeletedIdentity(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(fmt.Sprintf("startup=%v", startup), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "restart.db")
			database, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			resolved, err := resolvePersistentMailboxes(database, reviewMailboxes(), "Screener", "Junk")
			if err != nil {
				t.Fatal(err)
			}
			initialIdentity, err := database.GetState(mailboxIdentityKey)
			if err != nil {
				t.Fatal(err)
			}
			mailboxes := reviewMailboxes()[1:]
			// Missing or ambiguous replacement must leave the persisted identity intact.
			for _, invalid := range [][]jmap.Mailbox{
				mailboxes,
				append(append([]jmap.Mailbox{}, mailboxes...), jmap.Mailbox{ID: "duplicate-a", Name: "Screener", ParentID: stringPtr("junk")}, jmap.Mailbox{ID: "duplicate-b", Name: "Screener", ParentID: stringPtr("trash")}),
			} {
				if _, err := resolvePersistentMailboxes(database, invalid, "Screener", "Junk"); err == nil {
					t.Fatal("invalid replacement accepted")
				}
				identity, err := database.GetState(mailboxIdentityKey)
				if err != nil || identity != initialIdentity {
					t.Fatalf("invalid replacement changed identity: %s %v", identity, err)
				}
			}
			mailboxes = append(mailboxes, jmap.Mailbox{ID: "replacement", Name: "Screener"})
			if startup {
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
				database, err = db.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				resolved, err = resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				client, closeServer := monitorJMAPServer(t, func(method string, _ map[string]interface{}, w http.ResponseWriter) {
					if method != "Mailbox/get" {
						t.Errorf("unexpected %s", method)
					}
					writeMonitorJMAP(w, method, map[string]interface{}{"list": mailboxes})
				})
				defer closeServer()
				if err := refreshMailboxes(client, database, resolved, "Screener", "Junk", nil); err != nil {
					t.Fatal(err)
				}
				dirty, err := database.GetBoolState(sieveDirtyKey)
				if err != nil || !dirty {
					t.Fatalf("replacement not marked dirty: %v %v", dirty, err)
				}
			}
			if resolved.Screener.ID != "replacement" {
				t.Fatal("replacement identity not selected")
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			database, err = db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			// The persisted replacement remains selected after its own rename.
			mailboxes[len(mailboxes)-1].Name = "Screening"
			resolved, err = resolvePersistentMailboxes(database, mailboxes, "Screener", "Junk")
			if err != nil || resolved.Screener.ID != "replacement" {
				t.Fatalf("replacement lost after restart: %+v %v", resolved, err)
			}
		})
	}
}
