// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type SenderRoute struct {
	ID              int64
	SenderEmail     string
	TargetMailbox   string
	TargetMailboxID string
	CreatedAt       string
	UpdatedAt       string
}

type EmailCacheEntry struct {
	EmailID    string
	MailboxIDs []string
}

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

// --- Sender routes ---

func (db *DB) UpsertRoute(senderEmail, targetMailbox, targetMailboxID string) error {
	senderEmail = NormalizeSender(senderEmail)
	var existingID int64
	err := db.conn.QueryRow(`
		SELECT id
		FROM sender_routes
		WHERE lower(trim(sender_email)) = ?
		ORDER BY id ASC
		LIMIT 1
	`, senderEmail).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("find route for upsert: %w", err)
	}
	if err == nil {
		_, err = db.conn.Exec(`
			UPDATE sender_routes
			SET sender_email = ?,
				target_mailbox = ?,
				target_mailbox_id = ?,
				updated_at = datetime('now')
			WHERE id = ?
		`, senderEmail, targetMailbox, targetMailboxID, existingID)
		if err != nil {
			return fmt.Errorf("update route: %w", err)
		}
		return nil
	}

	_, err = db.conn.Exec(`
		INSERT INTO sender_routes (sender_email, target_mailbox, target_mailbox_id)
		VALUES (?, ?, ?)
		ON CONFLICT(sender_email) DO UPDATE SET
			target_mailbox   = excluded.target_mailbox,
			target_mailbox_id = excluded.target_mailbox_id,
			updated_at       = datetime('now')
	`, senderEmail, targetMailbox, targetMailboxID)
	if err != nil {
		return fmt.Errorf("upsert route: %w", err)
	}
	return nil
}

func (db *DB) DeleteRoute(senderEmail string) error {
	senderEmail = NormalizeSender(senderEmail)
	_, err := db.conn.Exec("DELETE FROM sender_routes WHERE lower(trim(sender_email)) = ?", senderEmail)
	if err != nil {
		return fmt.Errorf("delete route: %w", err)
	}
	return nil
}

func (db *DB) GetRoute(senderEmail string) (*SenderRoute, error) {
	senderEmail = NormalizeSender(senderEmail)
	row := db.conn.QueryRow(`
		SELECT id, sender_email, target_mailbox, target_mailbox_id, created_at, updated_at
		FROM sender_routes
		WHERE lower(trim(sender_email)) = ?
		ORDER BY id ASC
		LIMIT 1
	`, senderEmail)

	var r SenderRoute
	err := row.Scan(&r.ID, &r.SenderEmail, &r.TargetMailbox, &r.TargetMailboxID, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get route: %w", err)
	}
	return &r, nil
}

func (db *DB) GetAllRoutes() ([]SenderRoute, error) {
	rows, err := db.conn.Query(`
		SELECT id, sender_email, target_mailbox, target_mailbox_id, created_at, updated_at
		FROM sender_routes
		ORDER BY sender_email ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("get all routes: %w", err)
	}
	defer rows.Close()

	var routes []SenderRoute
	for rows.Next() {
		var r SenderRoute
		if err := rows.Scan(&r.ID, &r.SenderEmail, &r.TargetMailbox, &r.TargetMailboxID, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		routes = append(routes, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routes: %w", err)
	}
	return routes, nil
}

// --- Email mailbox cache ---

func (db *DB) UpsertEmailCache(emailID string, mailboxIDs []string) error {
	data, err := json.Marshal(mailboxIDs)
	if err != nil {
		return fmt.Errorf("marshal mailbox IDs: %w", err)
	}
	_, err = db.conn.Exec(`
		INSERT INTO email_mailbox_cache (email_id, mailbox_ids, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(email_id) DO UPDATE SET
			mailbox_ids = excluded.mailbox_ids,
			updated_at  = excluded.updated_at
	`, emailID, string(data))
	if err != nil {
		return fmt.Errorf("upsert email cache: %w", err)
	}
	return nil
}

func (db *DB) GetEmailCache(emailID string) (*EmailCacheEntry, error) {
	row := db.conn.QueryRow(`
		SELECT email_id, mailbox_ids
		FROM email_mailbox_cache
		WHERE email_id = ?
	`, emailID)

	var entry EmailCacheEntry
	var raw string
	err := row.Scan(&entry.EmailID, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get email cache: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &entry.MailboxIDs); err != nil {
		return nil, fmt.Errorf("unmarshal mailbox IDs: %w", err)
	}
	return &entry, nil
}

func (db *DB) DeleteEmailCache(emailID string) error {
	_, err := db.conn.Exec("DELETE FROM email_mailbox_cache WHERE email_id = ?", emailID)
	if err != nil {
		return fmt.Errorf("delete email cache: %w", err)
	}
	return nil
}

// --- Daemon state ---

func (db *DB) GetState(key string) (string, error) {
	row := db.conn.QueryRow("SELECT value FROM daemon_state WHERE key = ?", key)
	var value string
	err := row.Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get state: %w", err)
	}
	return value, nil
}

func (db *DB) SetState(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO daemon_state (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("set state: %w", err)
	}
	return nil
}

func (db *DB) GetBoolState(key string) (bool, error) {
	value, err := db.GetState(key)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

func (db *DB) SetBoolState(key string, value bool) error {
	if value {
		return db.SetState(key, "true")
	}
	return db.SetState(key, "false")
}

func NormalizeSender(sender string) string {
	return strings.ToLower(strings.TrimSpace(sender))
}

// --- Migration ---

func migrate(conn *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS sender_routes (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			sender_email     TEXT NOT NULL UNIQUE,
			target_mailbox   TEXT NOT NULL,
			target_mailbox_id TEXT NOT NULL,
			created_at       TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sender_routes_email ON sender_routes(sender_email)`,
		`CREATE TABLE IF NOT EXISTS email_mailbox_cache (
			email_id    TEXT PRIMARY KEY,
			mailbox_ids TEXT NOT NULL,
			updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS daemon_state (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("exec migrate statement: %w", err)
		}
	}
	return nil
}
