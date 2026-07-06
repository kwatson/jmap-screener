// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestDB_OpenCreatesTables(t *testing.T) {
	database := openTestDB(t)

	_, err := database.conn.Exec("INSERT INTO sender_routes (sender_email, target_mailbox, target_mailbox_id) VALUES ('a@b.com', 'Inbox', 'm1')")
	if err != nil {
		t.Fatalf("sender_routes table not created: %v", err)
	}
	_, err = database.conn.Exec("INSERT INTO email_mailbox_cache (email_id, mailbox_ids) VALUES ('e1', '[]')")
	if err != nil {
		t.Fatalf("email_mailbox_cache table not created: %v", err)
	}
	_, err = database.conn.Exec("INSERT INTO daemon_state (key, value) VALUES ('k1', 'v1')")
	if err != nil {
		t.Fatalf("daemon_state table not created: %v", err)
	}
}

func TestDB_UpsertRoute_Insert(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertRoute(" Alice@Example.COM ", "Family", "mbox_family")
	if err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	route, err := database.GetRoute("alice@example.com")
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if route == nil {
		t.Fatal("GetRoute returned nil for inserted sender")
	}
	if route.SenderEmail != "alice@example.com" {
		t.Errorf("SenderEmail = %q, want %q", route.SenderEmail, "alice@example.com")
	}
	if route.TargetMailbox != "Family" {
		t.Errorf("TargetMailbox = %q, want %q", route.TargetMailbox, "Family")
	}
	if route.TargetMailboxID != "mbox_family" {
		t.Errorf("TargetMailboxID = %q, want %q", route.TargetMailboxID, "mbox_family")
	}
	if route.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if route.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
}

func TestDB_RouteOperationsNormalizeSender(t *testing.T) {
	database := openTestDB(t)

	if err := database.UpsertRoute(" Mixed@Example.COM ", "Family", "mbox_family"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	route, err := database.GetRoute("mixed@example.com")
	if err != nil {
		t.Fatalf("GetRoute lowercase failed: %v", err)
	}
	if route == nil {
		t.Fatal("expected normalized lookup to find route")
	}
	if route.SenderEmail != "mixed@example.com" {
		t.Errorf("SenderEmail = %q, want normalized", route.SenderEmail)
	}
	route, err = database.GetRoute(" MIXED@EXAMPLE.COM ")
	if err != nil {
		t.Fatalf("GetRoute mixed failed: %v", err)
	}
	if route == nil {
		t.Fatal("expected mixed-case lookup to find route")
	}
	if err := database.DeleteRoute(" MIXED@EXAMPLE.COM "); err != nil {
		t.Fatalf("DeleteRoute failed: %v", err)
	}
	route, err = database.GetRoute("mixed@example.com")
	if err != nil {
		t.Fatalf("GetRoute after delete failed: %v", err)
	}
	if route != nil {
		t.Fatalf("expected route deleted, got %+v", route)
	}
}

func TestDB_RouteOperationsFindLegacyMixedCaseRows(t *testing.T) {
	database := openTestDB(t)
	_, err := database.conn.Exec(`
		INSERT INTO sender_routes (sender_email, target_mailbox, target_mailbox_id)
		VALUES (' Legacy@Example.COM ', 'Old', 'old_id')
	`)
	if err != nil {
		t.Fatalf("insert legacy route failed: %v", err)
	}

	route, err := database.GetRoute("legacy@example.com")
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if route == nil || route.TargetMailbox != "Old" {
		t.Fatalf("legacy route = %+v, want Old", route)
	}

	if err := database.UpsertRoute("legacy@example.com", "New", "new_id"); err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}
	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected legacy row updated in place, got %d routes: %+v", len(routes), routes)
	}
	if routes[0].SenderEmail != "legacy@example.com" || routes[0].TargetMailboxID != "new_id" {
		t.Fatalf("route after upsert = %+v", routes[0])
	}

	if err := database.DeleteRoute(" LEGACY@EXAMPLE.COM "); err != nil {
		t.Fatalf("DeleteRoute failed: %v", err)
	}
	route, err = database.GetRoute("legacy@example.com")
	if err != nil {
		t.Fatalf("GetRoute after delete failed: %v", err)
	}
	if route != nil {
		t.Fatalf("expected legacy route deleted, got %+v", route)
	}
}

func TestDB_UpsertRoute_Update(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertRoute("bob@example.com", "Inbox", "mbox_inbox")
	if err != nil {
		t.Fatalf("first UpsertRoute failed: %v", err)
	}

	first, err := database.GetRoute("bob@example.com")
	if err != nil {
		t.Fatalf("first GetRoute failed: %v", err)
	}
	originalCreatedAt := first.CreatedAt

	time.Sleep(10 * time.Millisecond)

	err = database.UpsertRoute("bob@example.com", "Projects", "mbox_projects")
	if err != nil {
		t.Fatalf("second UpsertRoute failed: %v", err)
	}

	second, err := database.GetRoute("bob@example.com")
	if err != nil {
		t.Fatalf("second GetRoute failed: %v", err)
	}
	if second.TargetMailbox != "Projects" {
		t.Errorf("TargetMailbox = %q, want %q", second.TargetMailbox, "Projects")
	}
	if second.TargetMailboxID != "mbox_projects" {
		t.Errorf("TargetMailboxID = %q, want %q", second.TargetMailboxID, "mbox_projects")
	}
	if second.CreatedAt != originalCreatedAt {
		t.Errorf("CreatedAt changed from %q to %q", originalCreatedAt, second.CreatedAt)
	}
}

func TestDB_GetRoute_NotFound(t *testing.T) {
	database := openTestDB(t)

	route, err := database.GetRoute("nobody@example.com")
	if err != nil {
		t.Fatalf("GetRoute returned unexpected error: %v", err)
	}
	if route != nil {
		t.Fatalf("GetRoute returned non-nil for unknown sender: %+v", route)
	}
}

func TestDB_GetRoute_Found(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertRoute("carol@example.com", "Work", "mbox_work")
	if err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	route, err := database.GetRoute("carol@example.com")
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if route == nil {
		t.Fatal("GetRoute returned nil for inserted sender")
	}
	if route.SenderEmail != "carol@example.com" {
		t.Errorf("SenderEmail = %q, want %q", route.SenderEmail, "carol@example.com")
	}
	if route.TargetMailbox != "Work" {
		t.Errorf("TargetMailbox = %q, want %q", route.TargetMailbox, "Work")
	}
	if route.TargetMailboxID != "mbox_work" {
		t.Errorf("TargetMailboxID = %q, want %q", route.TargetMailboxID, "mbox_work")
	}
	if route.ID == 0 {
		t.Error("ID should be non-zero")
	}
	if route.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if route.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty")
	}
}

func TestDB_GetAllRoutes_Empty(t *testing.T) {
	database := openTestDB(t)

	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestDB_GetAllRoutes_Multiple(t *testing.T) {
	database := openTestDB(t)

	database.UpsertRoute("z@example.com", "ZMail", "mbox_z")
	database.UpsertRoute("a@example.com", "AMail", "mbox_a")
	database.UpsertRoute("m@example.com", "MMail", "mbox_m")

	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	if routes[0].SenderEmail != "a@example.com" {
		t.Errorf("first route SenderEmail = %q, want %q", routes[0].SenderEmail, "a@example.com")
	}
	if routes[1].SenderEmail != "m@example.com" {
		t.Errorf("second route SenderEmail = %q, want %q", routes[1].SenderEmail, "m@example.com")
	}
	if routes[2].SenderEmail != "z@example.com" {
		t.Errorf("third route SenderEmail = %q, want %q", routes[2].SenderEmail, "z@example.com")
	}
}

func TestDB_DeleteRoute_Exists(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertRoute("dave@example.com", "Inbox", "mbox_inbox")
	if err != nil {
		t.Fatalf("UpsertRoute failed: %v", err)
	}

	err = database.DeleteRoute("dave@example.com")
	if err != nil {
		t.Fatalf("DeleteRoute failed: %v", err)
	}

	route, err := database.GetRoute("dave@example.com")
	if err != nil {
		t.Fatalf("GetRoute after delete returned error: %v", err)
	}
	if route != nil {
		t.Fatalf("GetRoute returned non-nil after delete: %+v", route)
	}
}

func TestDB_DeleteRoute_NotExists(t *testing.T) {
	database := openTestDB(t)

	err := database.DeleteRoute("nonexistent@example.com")
	if err != nil {
		t.Fatalf("DeleteRoute for non-existent sender returned error: %v", err)
	}
}

func TestDB_UpsertRoute_SameSenderTwice(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertRoute("eve@example.com", "First", "mbox_first")
	if err != nil {
		t.Fatalf("first UpsertRoute failed: %v", err)
	}

	err = database.UpsertRoute("eve@example.com", "Second", "mbox_second")
	if err != nil {
		t.Fatalf("second UpsertRoute failed: %v", err)
	}

	routes, err := database.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].TargetMailbox != "Second" {
		t.Errorf("TargetMailbox = %q, want %q", routes[0].TargetMailbox, "Second")
	}
}

func TestDB_UpsertEmailCache_New(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertEmailCache("email1", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("UpsertEmailCache failed: %v", err)
	}

	entry, err := database.GetEmailCache("email1")
	if err != nil {
		t.Fatalf("GetEmailCache failed: %v", err)
	}
	if entry == nil {
		t.Fatal("GetEmailCache returned nil for inserted entry")
	}
	if entry.EmailID != "email1" {
		t.Errorf("EmailID = %q, want %q", entry.EmailID, "email1")
	}
	if len(entry.MailboxIDs) != 2 {
		t.Fatalf("expected 2 mailbox IDs, got %d: %v", len(entry.MailboxIDs), entry.MailboxIDs)
	}
	if entry.MailboxIDs[0] != "m1" || entry.MailboxIDs[1] != "m2" {
		t.Errorf("MailboxIDs = %v, want [m1 m2]", entry.MailboxIDs)
	}
}

func TestDB_UpsertEmailCache_Update(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertEmailCache("email2", []string{"a", "b"})
	if err != nil {
		t.Fatalf("first UpsertEmailCache failed: %v", err)
	}

	err = database.UpsertEmailCache("email2", []string{"c", "d", "e"})
	if err != nil {
		t.Fatalf("second UpsertEmailCache failed: %v", err)
	}

	entry, err := database.GetEmailCache("email2")
	if err != nil {
		t.Fatalf("GetEmailCache failed: %v", err)
	}
	if entry == nil {
		t.Fatal("GetEmailCache returned nil")
	}
	if len(entry.MailboxIDs) != 3 {
		t.Fatalf("expected 3 mailbox IDs, got %d: %v", len(entry.MailboxIDs), entry.MailboxIDs)
	}
	if entry.MailboxIDs[0] != "c" {
		t.Errorf("MailboxIDs[0] = %q, want %q", entry.MailboxIDs[0], "c")
	}
}

func TestDB_GetEmailCache_NotFound(t *testing.T) {
	database := openTestDB(t)

	entry, err := database.GetEmailCache("nonexistent")
	if err != nil {
		t.Fatalf("GetEmailCache returned unexpected error: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEmailCache returned non-nil for unknown email: %+v", entry)
	}
}

func TestDB_GetEmailCache_EmptyMailboxes(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertEmailCache("email_empty", []string{})
	if err != nil {
		t.Fatalf("UpsertEmailCache with empty slice failed: %v", err)
	}

	entry, err := database.GetEmailCache("email_empty")
	if err != nil {
		t.Fatalf("GetEmailCache failed: %v", err)
	}
	if entry == nil {
		t.Fatal("GetEmailCache returned nil")
	}
	if entry.EmailID != "email_empty" {
		t.Errorf("EmailID = %q, want %q", entry.EmailID, "email_empty")
	}
	if len(entry.MailboxIDs) != 0 {
		t.Errorf("expected empty MailboxIDs, got %v", entry.MailboxIDs)
	}
}

func TestDB_DeleteEmailCache_Exists(t *testing.T) {
	database := openTestDB(t)

	err := database.UpsertEmailCache("email_del", []string{"x"})
	if err != nil {
		t.Fatalf("UpsertEmailCache failed: %v", err)
	}

	err = database.DeleteEmailCache("email_del")
	if err != nil {
		t.Fatalf("DeleteEmailCache failed: %v", err)
	}

	entry, err := database.GetEmailCache("email_del")
	if err != nil {
		t.Fatalf("GetEmailCache after delete returned error: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEmailCache returned non-nil after delete: %+v", entry)
	}
}

func TestDB_DeleteEmailCache_NotExists(t *testing.T) {
	database := openTestDB(t)

	err := database.DeleteEmailCache("does_not_exist")
	if err != nil {
		t.Fatalf("DeleteEmailCache for non-existent entry returned error: %v", err)
	}
}

func TestDB_GetState_NotFound(t *testing.T) {
	database := openTestDB(t)

	value, err := database.GetState("unknown_key")
	if err != nil {
		t.Fatalf("GetState returned unexpected error: %v", err)
	}
	if value != "" {
		t.Errorf("GetState should return empty string for unknown key, got %q", value)
	}
}

func TestDB_SetState_New(t *testing.T) {
	database := openTestDB(t)

	err := database.SetState("key1", "value1")
	if err != nil {
		t.Fatalf("SetState failed: %v", err)
	}

	value, err := database.GetState("key1")
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if value != "value1" {
		t.Errorf("GetState = %q, want %q", value, "value1")
	}
}

func TestDB_SetState_Update(t *testing.T) {
	database := openTestDB(t)

	err := database.SetState("key2", "initial")
	if err != nil {
		t.Fatalf("first SetState failed: %v", err)
	}

	err = database.SetState("key2", "updated")
	if err != nil {
		t.Fatalf("second SetState failed: %v", err)
	}

	value, err := database.GetState("key2")
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if value != "updated" {
		t.Errorf("GetState = %q, want %q", value, "updated")
	}
}

func TestDB_State_EmptyString(t *testing.T) {
	database := openTestDB(t)

	err := database.SetState("empty_key", "")
	if err != nil {
		t.Fatalf("SetState with empty string failed: %v", err)
	}

	value, err := database.GetState("empty_key")
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if value != "" {
		t.Errorf("GetState = %q, want empty string", value)
	}
}

func TestDB_Close(t *testing.T) {
	database := openTestDB(t)

	database.Close()

	// Verify operations fail after close
	_, err := database.GetState("any")
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}
