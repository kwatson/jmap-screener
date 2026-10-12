// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/kwatson/jmap-screener/internal/db"
	"github.com/kwatson/jmap-screener/internal/jmap"
)

func TestDiscoverSessionForStartupConfiguresUploadURLBeforeSieveUpload(t *testing.T) {
	var uploadHit atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/jmap":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"uploadUrl": server.URL + "/custom/{accountId}/upload",
				"primaryAccounts": map[string]string{
					jmap.CapMail: "acc1",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/custom/acc1/upload":
			uploadHit.Store(true)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accountId": "acc1",
				"blobId":    "blob1",
				"type":      "application/sieve",
				"size":      10,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/jmap/":
			writeMonitorJMAP(w, "SieveScript/set", map[string]interface{}{
				"accountId": "acc1",
				"created": map[string]interface{}{
					"script": map[string]interface{}{"id": "s1"},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := jmap.NewClient(server.URL+"/jmap/", "token", "")
	discoverSessionForStartup(client, "")
	if client.AccountID != "acc1" {
		t.Fatalf("AccountID = %q, want discovered acc1", client.AccountID)
	}
	if err := client.CreateSieveScript("screener", "require [];", true); err != nil {
		t.Fatalf("CreateSieveScript failed: %v", err)
	}
	if !uploadHit.Load() {
		t.Fatal("expected upload to use discovered uploadUrl")
	}
}

func TestExtractSender_FromPresent(t *testing.T) {
	email := jmap.Email{
		From: []jmap.Emailer{
			{Name: "Alice", Email: "alice@example.com"},
		},
	}
	result := extractSender(email)
	if result != "alice@example.com" {
		t.Errorf("extractSender = %q, want %q", result, "alice@example.com")
	}
}

func TestExtractSender_FromEmpty(t *testing.T) {
	email := jmap.Email{
		From:   []jmap.Emailer{},
		Sender: []jmap.Emailer{{Name: "Bob", Email: "bob@example.com"}},
	}
	result := extractSender(email)
	if result != "bob@example.com" {
		t.Errorf("extractSender = %q, want %q", result, "bob@example.com")
	}
}

func TestExtractSender_BothEmpty(t *testing.T) {
	email := jmap.Email{
		From:   []jmap.Emailer{},
		Sender: []jmap.Emailer{},
	}
	result := extractSender(email)
	if result != "" {
		t.Errorf("extractSender = %q, want empty string", result)
	}
}

func TestExtractSender_FromEmptyString(t *testing.T) {
	email := jmap.Email{
		From:   []jmap.Emailer{{Name: "X", Email: ""}},
		Sender: []jmap.Emailer{{Name: "Y", Email: "sender@example.com"}},
	}
	result := extractSender(email)
	if result != "sender@example.com" {
		t.Errorf("extractSender = %q, want %q", result, "sender@example.com")
	}
}

func TestExtractSender_NoFields(t *testing.T) {
	email := jmap.Email{}
	result := extractSender(email)
	if result != "" {
		t.Errorf("extractSender = %q, want empty string", result)
	}
}

func TestContains_Found(t *testing.T) {
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("contains should return true")
	}
}

func TestContains_NotFound(t *testing.T) {
	if contains([]string{"a", "b", "c"}, "x") {
		t.Error("contains should return false")
	}
}

func TestContains_EmptySlice(t *testing.T) {
	if contains([]string{}, "a") {
		t.Error("contains should return false for empty slice")
	}
}

func TestContains_FirstAndLast(t *testing.T) {
	slice := []string{"first", "middle", "last"}
	if !contains(slice, "first") {
		t.Error("contains should find first element")
	}
	if !contains(slice, "last") {
		t.Error("contains should find last element")
	}
}

func TestKeys_Normal(t *testing.T) {
	m := map[string]bool{"a": true, "b": false, "c": true}
	result := keys(m)
	if len(result) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(result), result)
	}
	found := make(map[string]bool)
	for _, k := range result {
		found[k] = true
	}
	if !found["a"] || !found["b"] || !found["c"] {
		t.Errorf("missing expected keys in result: %v", result)
	}
}

func TestKeys_Empty(t *testing.T) {
	m := map[string]bool{}
	result := keys(m)
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestKeys_Single(t *testing.T) {
	m := map[string]bool{"only": true}
	result := keys(m)
	if len(result) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(result), result)
	}
	if result[0] != "only" {
		t.Errorf("key = %q, want %q", result[0], "only")
	}
}

func TestFindNewMailbox_SingleNew(t *testing.T) {
	curr := []string{"a", "b", "c"}
	result := findNewMailbox(curr, "a")
	if result != "" {
		t.Errorf("findNewMailbox = %q, want empty string for multiple candidates", result)
	}
}

func TestFindNewMailbox_MultipleNew(t *testing.T) {
	curr := []string{"a", "b", "c"}
	result := findNewMailbox(curr, "a")
	if result != "" {
		t.Errorf("findNewMailbox = %q, want empty string for multiple candidates", result)
	}
}

func TestFindNewMailbox_NoNew(t *testing.T) {
	curr := []string{"b"}
	result := findNewMailbox(curr, "a")
	if result != "b" {
		t.Errorf("findNewMailbox = %q, want %q", result, "b")
	}
}

func TestFindNewMailbox_AllNew(t *testing.T) {
	curr := []string{"a", "b"}
	result := findNewMailbox(curr, "a")
	if result != "b" {
		t.Errorf("findNewMailbox = %q, want %q", result, "b")
	}
}

func TestIsExcluded_Match(t *testing.T) {
	if !isExcluded("mailer-daemon@local", []string{"mailer-daemon@", "postmaster@"}) {
		t.Error("isExcluded should return true for prefix match")
	}
}

func TestIsExcluded_NoPrefixMatch(t *testing.T) {
	if isExcluded("not-mailer-daemon@x.com", []string{"mailer-daemon@"}) {
		t.Error("isExcluded should return false when sender does not start with pattern")
	}
}

func TestIsExcluded_CaseInsensitive(t *testing.T) {
	if !isExcluded("MAILER-DAEMON@local", []string{"mailer-daemon@"}) {
		t.Error("isExcluded should return true for case-insensitive prefix match")
	}
}

func TestIsExcluded_EmptyExcluded(t *testing.T) {
	if isExcluded("user@example.com", []string{}) {
		t.Error("isExcluded should return false for empty excluded list")
	}
}

func TestBuildMailboxIndex_NestedPathsAndRoles(t *testing.T) {
	parentID := "inbox"
	junkRole := "junk"
	trashRole := "trash"
	mailboxes := []jmap.Mailbox{
		{ID: "inbox", Name: "INBOX"},
		{ID: "family", Name: "Family", ParentID: &parentID},
		{ID: "bills", Name: "Bills", ParentID: stringPtr("family")},
		{ID: "junk", Name: "Spam", Role: &junkRole},
		{ID: "trash", Name: "Deleted", Role: &trashRole},
	}
	resolved, err := ResolveMailboxes(mailboxes, "INBOX/Family/Bills", "Junk")
	if err != nil {
		t.Fatalf("ResolveMailboxes failed: %v", err)
	}
	if resolved.Screener.ID != "bills" {
		t.Fatalf("Screener.ID = %q, want bills", resolved.Screener.ID)
	}
	if resolved.Screener.DisplayPath != "INBOX/Family/Bills" {
		t.Errorf("DisplayPath = %q", resolved.Screener.DisplayPath)
	}
	if resolved.Screener.SievePath != "INBOX.Family.Bills" {
		t.Errorf("SievePath = %q", resolved.Screener.SievePath)
	}
	if resolved.Junk == nil || resolved.Junk.ID != "junk" {
		t.Fatalf("Junk = %+v, want junk role mailbox", resolved.Junk)
	}
	if resolved.Junk.SievePath != "Spam" {
		t.Errorf("Junk.SievePath = %q, want Spam", resolved.Junk.SievePath)
	}
	if resolved.Trash == nil || resolved.Trash.ID != "trash" {
		t.Fatalf("Trash = %+v, want trash role mailbox", resolved.Trash)
	}
	if resolved.Trash.SievePath != "Deleted" {
		t.Errorf("Trash.SievePath = %q, want Deleted", resolved.Trash.SievePath)
	}
}

func TestResolveMailboxes_DuplicateConfiguredName(t *testing.T) {
	mailboxes := []jmap.Mailbox{
		{ID: "p1", Name: "Parent1"},
		{ID: "p2", Name: "Parent2"},
		{ID: "s1", Name: "Screener", ParentID: stringPtr("p1")},
		{ID: "s2", Name: "Screener", ParentID: stringPtr("p2")},
	}
	if _, err := ResolveMailboxes(mailboxes, "Screener", "Junk"); err == nil {
		t.Fatal("expected duplicate configured screener name error")
	}
}

func TestResolveMailboxes_DuplicateRole(t *testing.T) {
	junkRole := "junk"
	mailboxes := []jmap.Mailbox{
		{ID: "screener", Name: "Screener"},
		{ID: "junk1", Name: "Junk", Role: &junkRole},
		{ID: "junk2", Name: "Spam", Role: &junkRole},
	}
	if _, err := ResolveMailboxes(mailboxes, "Screener", "Junk"); err == nil {
		t.Fatal("expected duplicate junk role error")
	}
}

func TestBuildMailboxIndex_Cycle(t *testing.T) {
	mailboxes := []jmap.Mailbox{
		{ID: "a", Name: "A", ParentID: stringPtr("b")},
		{ID: "b", Name: "B", ParentID: stringPtr("a")},
	}
	if _, err := BuildMailboxIndex(mailboxes); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestApplyEmailTransition_MoveIntoScreenerDeletesExistingRoute(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("sender@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "Sender@Example.COM", "screener"), []string{"target"}, []string{"screener"}, nil)
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "sender@example.com")
	assertSieveDirty(t, database)
}

func TestApplyEmailTransition_JunkToScreenerDeletesRouteAndDoesNotLearnScreener(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("sender@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "sender@example.com", "screener"), []string{"junk"}, []string{"screener"}, nil)
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "sender@example.com")
}

func TestApplyEmailTransition_JunkAndScreenerToScreenerDeletesRoute(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("sender@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "sender@example.com", "screener"), []string{"junk", "screener"}, []string{"screener"}, nil)
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "sender@example.com")
}

func TestApplyEmailTransition_ExcludedSenderIntoScreenerStillDeletesRoute(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("mailer-daemon@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "mailer-daemon@example.com", "screener"), []string{"target"}, []string{"screener"}, []string{"mailer-daemon@"})
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "mailer-daemon@example.com")
}

func TestApplyEmailTransition_ScreenerToJunkStillLearnsJunkRoute(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "sender@example.com", "junk"), []string{"screener"}, []string{"junk"}, nil)
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertRouteTarget(t, database, "sender@example.com", "junk")
}

func TestApplyEmailTransition_JunkToNormalFolderStillLearns(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "sender@example.com", "target"), []string{"junk"}, []string{"target"}, nil)
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertRouteTarget(t, database, "sender@example.com", "target")
}

func TestApplyEmailTransition_ScreenerToTrashDeletionIgnoresExcludedSenders(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("mailer-daemon@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := applyEmailTransition(database, resolved, monitorEmail("e1", "mailer-daemon@example.com", "trash"), []string{"screener"}, []string{"trash"}, []string{"mailer-daemon@"})
	if err != nil {
		t.Fatalf("applyEmailTransition failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "mailer-daemon@example.com")
}

func TestProcessEmailChangeUpdatesCacheAfterDeleteAndSkipPaths(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("sender@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	if err := database.UpsertEmailCache("delete-path", []string{"target"}); err != nil {
		t.Fatalf("UpsertEmailCache delete-path failed: %v", err)
	}
	if err := database.UpsertEmailCache("skip-path", []string{"screener"}); err != nil {
		t.Fatalf("UpsertEmailCache skip-path failed: %v", err)
	}

	if _, err := processEmailChange(database, resolved, monitorEmail("delete-path", "sender@example.com", "screener"), nil); err != nil {
		t.Fatalf("processEmailChange delete-path failed: %v", err)
	}
	entry, err := database.GetEmailCache("delete-path")
	if err != nil {
		t.Fatalf("GetEmailCache delete-path failed: %v", err)
	}
	if entry == nil || !contains(entry.MailboxIDs, "screener") {
		t.Fatalf("delete-path cache = %+v, want screener", entry)
	}

	if _, err := processEmailChange(database, resolved, monitorEmail("skip-path", "mailer-daemon@example.com", "target"), []string{"mailer-daemon@"}); err != nil {
		t.Fatalf("processEmailChange skip-path failed: %v", err)
	}
	entry, err = database.GetEmailCache("skip-path")
	if err != nil {
		t.Fatalf("GetEmailCache skip-path failed: %v", err)
	}
	if entry == nil || !contains(entry.MailboxIDs, "target") {
		t.Fatalf("skip-path cache = %+v, want target", entry)
	}
	assertNoRoute(t, database, "mailer-daemon@example.com")
}

func TestProcessEmailChangeUncachedMessageInScreenerDeletesExistingRoute(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	if err := database.UpsertRoute("sender@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	changed, err := processEmailChange(database, resolved, monitorEmail("uncached", "sender@example.com", "screener"), nil)
	if err != nil {
		t.Fatalf("processEmailChange failed: %v", err)
	}
	if !changed {
		t.Fatal("expected route change")
	}
	assertNoRoute(t, database, "sender@example.com")
	assertSieveDirty(t, database)
	entry, err := database.GetEmailCache("uncached")
	if err != nil {
		t.Fatalf("GetEmailCache failed: %v", err)
	}
	if entry == nil || !contains(entry.MailboxIDs, "screener") {
		t.Fatalf("cache entry = %+v, want screener", entry)
	}
}

func TestBootstrapCacheCachesWithoutLearningRoutes(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/get":
			ids := args["ids"].([]interface{})
			if len(ids) == 0 {
				writeMonitorJMAP(w, method, map[string]interface{}{
					"accountId": "acc1",
					"state":     "baseline-state",
					"list":      []interface{}{},
					"notFound":  []interface{}{},
				})
				return
			}
			list := make([]map[string]interface{}, 0, len(ids))
			for _, rawID := range ids {
				id := rawID.(string)
				mailboxID := "screener"
				if id == "e2" {
					mailboxID = "junk"
				}
				list = append(list, map[string]interface{}{
					"id":         id,
					"mailboxIds": map[string]interface{}{mailboxID: true},
					"from":       []map[string]interface{}{{"email": "Sender@Example.COM"}},
				})
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "scan-state",
				"list":      list,
				"notFound":  []interface{}{},
			})
		case "Email/query":
			filter := args["filter"].(map[string]interface{})
			mailboxID := filter["inMailbox"].(string)
			ids := []interface{}{}
			if mailboxID == "screener" {
				ids = []interface{}{"e1"}
			}
			if mailboxID == "junk" {
				ids = []interface{}{"e2"}
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        ids,
				"position":   0,
				"total":      len(ids),
			})
		case "Email/changes":
			if args["sinceState"].(string) != "baseline-state" {
				t.Fatalf("sinceState = %q, want baseline-state", args["sinceState"])
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":      "acc1",
				"oldState":       "baseline-state",
				"newState":       "email-state",
				"hasMoreChanges": false,
				"changed":        []interface{}{},
				"removed":        []interface{}{},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	state, err := bootstrapCache(client, database, resolved)
	if err != nil {
		t.Fatalf("bootstrapCache failed: %v", err)
	}
	if state != "email-state" {
		t.Errorf("state = %q", state)
	}
	if entry, _ := database.GetEmailCache("e1"); entry == nil {
		t.Fatal("expected screener email cached")
	}
	if entry, _ := database.GetEmailCache("e2"); entry == nil {
		t.Fatal("expected junk email cached")
	}
	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("bootstrap learned routes: %+v", routes)
	}
}

func TestBootstrapCacheGetsStateForEmptyMailboxes(t *testing.T) {
	database := openMonitorTestDB(t)
	resolved := monitorResolvedForWorkflow()
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/get":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "baseline-empty-state",
				"list":      []interface{}{},
				"notFound":  []interface{}{},
			})
		case "Email/query":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        []interface{}{},
				"position":   0,
				"total":      0,
			})
		case "Email/changes":
			if args["sinceState"].(string) != "baseline-empty-state" {
				t.Fatalf("sinceState = %q, want baseline-empty-state", args["sinceState"])
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":      "acc1",
				"oldState":       "baseline-empty-state",
				"newState":       "empty-state",
				"hasMoreChanges": false,
				"changed":        []interface{}{},
				"removed":        []interface{}{},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	state, err := bootstrapCache(client, database, resolved)
	if err != nil {
		t.Fatalf("bootstrapCache failed: %v", err)
	}
	if state != "empty-state" {
		t.Fatalf("state = %q, want empty-state", state)
	}
}

func TestBootstrapCacheReconcilesPagedChangesAndRemovedIDs(t *testing.T) {
	database := openMonitorTestDB(t)
	if err := database.UpsertEmailCache("removed", []string{"screener"}); err != nil {
		t.Fatalf("UpsertEmailCache failed: %v", err)
	}
	resolved := monitorResolvedForWorkflow()
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/get":
			ids := args["ids"].([]interface{})
			if len(ids) == 0 {
				writeMonitorJMAP(w, method, map[string]interface{}{
					"accountId": "acc1",
					"state":     "baseline-state",
					"list":      []interface{}{},
					"notFound":  []interface{}{},
				})
				return
			}
			if len(ids) != 1 || ids[0].(string) != "new" {
				t.Fatalf("Email/get ids = %v, want [new]", ids)
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "after-first-page",
				"list": []map[string]interface{}{{
					"id":         "new",
					"mailboxIds": map[string]interface{}{"screener": true},
				}},
				"notFound": []interface{}{},
			})
		case "Email/query":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        []interface{}{},
				"position":   0,
				"total":      0,
			})
		case "Email/changes":
			sinceState := args["sinceState"].(string)
			if sinceState == "baseline-state" {
				writeMonitorJMAP(w, method, map[string]interface{}{
					"accountId":      "acc1",
					"oldState":       "baseline-state",
					"newState":       "after-first-page",
					"hasMoreChanges": true,
					"changed":        []interface{}{"new"},
					"removed":        []interface{}{},
				})
				return
			}
			if sinceState != "after-first-page" {
				t.Fatalf("sinceState = %q, want after-first-page", sinceState)
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":      "acc1",
				"oldState":       "after-first-page",
				"newState":       "final-state",
				"hasMoreChanges": false,
				"changed":        []interface{}{},
				"removed":        []interface{}{"removed"},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	state, err := bootstrapCache(client, database, resolved)
	if err != nil {
		t.Fatalf("bootstrapCache failed: %v", err)
	}
	if state != "final-state" {
		t.Fatalf("state = %q, want final-state", state)
	}
	if entry, _ := database.GetEmailCache("new"); entry == nil || !contains(entry.MailboxIDs, "screener") {
		t.Fatalf("new cache entry = %+v, want screener", entry)
	}
	if entry, _ := database.GetEmailCache("removed"); entry != nil {
		t.Fatalf("removed cache entry still present: %+v", entry)
	}
	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("bootstrap learned routes: %+v", routes)
	}
	dirty, err := database.GetBoolState(sieveDirtyKey)
	if err != nil {
		t.Fatalf("GetBoolState failed: %v", err)
	}
	if dirty {
		t.Fatal("bootstrap marked sieve dirty")
	}
}

func TestRescueJunkPageMovesKnownSenderAndUpdatesCache(t *testing.T) {
	database := openMonitorTestDB(t)
	if err := database.UpsertRoute("known@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	resolved := monitorResolvedForWorkflow()
	moveSeen := false
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/query":
			ids := []interface{}{"e1"}
			if moveSeen {
				ids = []interface{}{}
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        ids,
				"position":   0,
				"total":      len(ids),
			})
		case "Email/get":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "email-state",
				"list": []map[string]interface{}{{
					"id":         "e1",
					"mailboxIds": map[string]interface{}{"junk": true},
					"from":       []map[string]interface{}{{"email": "Known@Example.COM"}},
				}},
				"notFound": []interface{}{},
			})
		case "Email/set":
			update := args["update"].(map[string]interface{})
			patch := update["e1"].(map[string]interface{})
			mailboxIDs := patch["mailboxIds"].(map[string]interface{})
			if _, ok := mailboxIDs["target"]; !ok {
				t.Fatalf("move target mailboxIds = %v", mailboxIDs)
			}
			moveSeen = true
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"updated":   map[string]interface{}{"e1": map[string]interface{}{}},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	moved, err := rescueJunkPage(client, database, resolved, 10)
	if err != nil {
		t.Fatalf("rescueJunkPage failed: %v", err)
	}
	if !moved || !moveSeen {
		t.Fatalf("expected rescue move, moved=%v moveSeen=%v", moved, moveSeen)
	}
	entry, err := database.GetEmailCache("e1")
	if err != nil {
		t.Fatalf("GetEmailCache failed: %v", err)
	}
	if entry == nil || len(entry.MailboxIDs) != 1 || entry.MailboxIDs[0] != "target" {
		t.Fatalf("cache entry = %+v, want target", entry)
	}
}

func TestRescueJunkPagePaginatesPastUnknownSenders(t *testing.T) {
	database := openMonitorTestDB(t)
	if err := database.UpsertRoute("known@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	resolved := monitorResolvedForWorkflow()
	moved := false
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/query":
			position := int(args["position"].(float64))
			ids := []interface{}{}
			total := 0
			if !moved {
				total = 2
				if position == 0 {
					ids = []interface{}{"unknown"}
				} else if position == 1 {
					ids = []interface{}{"known"}
				}
			} else if position == 0 {
				total = 1
				ids = []interface{}{"unknown"}
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        ids,
				"position":   position,
				"total":      total,
			})
		case "Email/get":
			ids := args["ids"].([]interface{})
			list := make([]map[string]interface{}, 0, len(ids))
			for _, rawID := range ids {
				id := rawID.(string)
				sender := "unknown@example.com"
				if id == "known" {
					sender = "known@example.com"
				}
				list = append(list, map[string]interface{}{
					"id":         id,
					"mailboxIds": map[string]interface{}{"junk": true},
					"from":       []map[string]interface{}{{"email": sender}},
				})
			}
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "email-state",
				"list":      list,
				"notFound":  []interface{}{},
			})
		case "Email/set":
			update := args["update"].(map[string]interface{})
			if _, ok := update["known"]; !ok {
				t.Fatalf("update = %v, want known", update)
			}
			moved = true
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"updated":   map[string]interface{}{"known": map[string]interface{}{}},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	rescueMoved, err := rescueJunkPage(client, database, resolved, 1)
	if err != nil {
		t.Fatalf("rescueJunkPage failed: %v", err)
	}
	if !rescueMoved || !moved {
		t.Fatalf("expected rescue move, rescueMoved=%v moved=%v", rescueMoved, moved)
	}
}

func TestRescueJunkPageTerminatesOnNotUpdated(t *testing.T) {
	database := openMonitorTestDB(t)
	if err := database.UpsertRoute("known@example.com", "Family", "target"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	resolved := monitorResolvedForWorkflow()
	queryCount := 0
	client, closeServer := monitorJMAPServer(t, func(method string, args map[string]interface{}, w http.ResponseWriter) {
		switch method {
		case "Email/query":
			queryCount++
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId":  "acc1",
				"queryState": "q1",
				"ids":        []interface{}{"known"},
				"position":   0,
				"total":      1,
			})
		case "Email/get":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"state":     "email-state",
				"list": []map[string]interface{}{{
					"id":         "known",
					"mailboxIds": map[string]interface{}{"junk": true},
					"from":       []map[string]interface{}{{"email": "known@example.com"}},
				}},
				"notFound": []interface{}{},
			})
		case "Email/set":
			writeMonitorJMAP(w, method, map[string]interface{}{
				"accountId": "acc1",
				"notUpdated": map[string]interface{}{
					"known": map[string]interface{}{"type": "forbidden", "description": "nope"},
				},
			})
		default:
			t.Fatalf("unexpected method %s", method)
		}
	})
	defer closeServer()

	moved, err := rescueJunkPage(client, database, resolved, 1)
	if err != nil {
		t.Fatalf("rescueJunkPage failed: %v", err)
	}
	if moved {
		t.Fatal("notUpdated should not count as moved")
	}
	if queryCount != 1 {
		t.Fatalf("queryCount = %d, want 1", queryCount)
	}
}

func stringPtr(v string) *string {
	return &v
}

func openMonitorTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open DB failed: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func monitorResolvedForWorkflow() *ResolvedMailboxes {
	index := &MailboxIndex{
		ByID: map[string]MailboxInfo{
			"screener": {ID: "screener", Name: "Screener", DisplayPath: "Screener", SievePath: "Screener"},
			"junk":     {ID: "junk", Name: "Junk", Role: "junk", DisplayPath: "Junk", SievePath: "Junk"},
			"trash":    {ID: "trash", Name: "Trash", Role: "trash", DisplayPath: "Trash", SievePath: "Trash"},
			"target":   {ID: "target", Name: "Family", DisplayPath: "Family", SievePath: "Family"},
		},
	}
	junk := index.ByID["junk"]
	trash := index.ByID["trash"]
	return &ResolvedMailboxes{
		Index:        index,
		Screener:     index.ByID["screener"],
		Junk:         &junk,
		Trash:        &trash,
		ScreenerPath: "Screener",
	}
}

func monitorEmail(id, sender, mailboxID string) jmap.Email {
	return jmap.Email{
		ID:         id,
		MailboxIDs: map[string]bool{mailboxID: true},
		From:       []jmap.Emailer{{Email: sender}},
	}
}

func assertNoRoute(t *testing.T, database *db.DB, sender string) {
	t.Helper()
	route, err := database.GetRoute(sender)
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if route != nil {
		t.Fatalf("route for %s = %+v, want nil", sender, route)
	}
}

func assertRouteTarget(t *testing.T, database *db.DB, sender, targetID string) {
	t.Helper()
	route, err := database.GetRoute(sender)
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if route == nil {
		t.Fatalf("route for %s = nil, want target %s", sender, targetID)
	}
	if route.TargetMailboxID != targetID {
		t.Fatalf("route target for %s = %q, want %q", sender, route.TargetMailboxID, targetID)
	}
}

func assertSieveDirty(t *testing.T, database *db.DB) {
	t.Helper()
	dirty, err := database.GetBoolState(sieveDirtyKey)
	if err != nil {
		t.Fatalf("GetBoolState failed: %v", err)
	}
	if !dirty {
		t.Fatal("expected sieve dirty")
	}
}

func monitorJMAPServer(t *testing.T, handler func(method string, args map[string]interface{}, w http.ResponseWriter)) (*jmap.Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jmap.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method := req.MethodCalls[0][0].(string)
		args := req.MethodCalls[0][1].(map[string]interface{})
		handler(method, args, w)
	}))
	return jmap.NewClient(server.URL, "token", "acc1"), server.Close
}

func writeMonitorJMAP(w http.ResponseWriter, method string, result map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jmap.Response{
		MethodResponses: []jmap.MethodResponse{{method, result, "c1"}},
	})
}
