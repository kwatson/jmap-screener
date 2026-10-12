// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/kwatson/jmap-screener/internal/db"
	"github.com/kwatson/jmap-screener/internal/jmap"
	"github.com/kwatson/jmap-screener/internal/sieve"
)

type MailboxInfo struct {
	ID          string
	Name        string
	Role        string
	ParentID    string
	DisplayPath string
	SievePath   string
}

type MailboxIndex struct {
	ByID          map[string]MailboxInfo
	byName        map[string][]MailboxInfo
	byDisplayPath map[string]MailboxInfo
	byRole        map[string][]MailboxInfo
}

type ResolvedMailboxes struct {
	Index        *MailboxIndex
	Screener     MailboxInfo
	Junk         *MailboxInfo
	Trash        *MailboxInfo
	ScreenerPath string
}

// Mailbox identities are tied to their configured selectors. A renamed folder
// keeps its identity across restart; changing the selector selects a new folder.
type mailboxIdentity struct {
	Selector string `json:"selector"`
	ID       string `json:"id"`
}
type mailboxIdentities struct {
	Screener mailboxIdentity `json:"screener"`
	Junk     mailboxIdentity `json:"junk"`
}

const mailboxIdentityKey = "mailbox_identities"

func resolveWithIdentity(database *db.DB, mailboxes []jmap.Mailbox, screenerName, junkName string) (*ResolvedMailboxes, error) {
	raw, err := database.GetState(mailboxIdentityKey)
	if err != nil {
		return nil, err
	}
	var identities mailboxIdentities
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &identities); err != nil {
			return nil, fmt.Errorf("decode mailbox identities: %w", err)
		}
	}
	idx, err := BuildMailboxIndex(mailboxes)
	if err != nil {
		return nil, err
	}
	resolve := func(selector string, saved mailboxIdentity) (MailboxInfo, error) {
		if saved.Selector == selector && saved.ID != "" {
			if info, ok := idx.ByID[saved.ID]; ok {
				return info, nil
			}
		}
		return idx.resolveConfigured(selector)
	}
	screener, err := resolve(screenerName, identities.Screener)
	if err != nil {
		return nil, fmt.Errorf("resolve screener mailbox: %w", err)
	}
	junk, err := idx.resolveRoleOrConfigured("junk", junkName)
	if len(idx.byRole["junk"]) == 0 && identities.Junk.Selector == junkName && identities.Junk.ID != "" {
		info, ok := idx.ByID[identities.Junk.ID]
		if ok {
			junk = &info
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	trash, err := idx.resolveRoleOrConfigured("trash", "Trash")
	if err != nil {
		return nil, err
	}
	return &ResolvedMailboxes{Index: idx, Screener: screener, ScreenerPath: screener.SievePath, Junk: junk, Trash: trash}, nil
}

func saveMailboxIdentities(database *db.DB, resolved *ResolvedMailboxes, screenerName, junkName string) error {
	identities := mailboxIdentities{Screener: mailboxIdentity{Selector: screenerName, ID: resolved.Screener.ID}}
	// Role-selected Junk follows the role, rather than pinning a previous role holder.
	if resolved.Junk != nil && resolved.Junk.Role != "junk" {
		identities.Junk = mailboxIdentity{Selector: junkName, ID: resolved.Junk.ID}
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		return err
	}
	return database.SetState(mailboxIdentityKey, string(raw))
}

func resolvePersistentMailboxes(database *db.DB, mailboxes []jmap.Mailbox, screenerName, junkName string) (*ResolvedMailboxes, error) {
	resolved, err := resolveWithIdentity(database, mailboxes, screenerName, junkName)
	if err != nil {
		return nil, err
	}
	if err := saveMailboxIdentities(database, resolved, screenerName, junkName); err != nil {
		return nil, err
	}
	return resolved, nil
}

func refreshUnknownMailboxes(client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes, emails []jmap.Email, screenerName, junkName string) error {
	unknown := make(map[string]bool)
	for _, email := range emails {
		for id := range email.MailboxIDs {
			if _, ok := resolved.Index.ByID[id]; !ok {
				unknown[id] = true
			}
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return refreshMailboxes(client, database, resolved, screenerName, junkName, unknown)
}

func refreshMailboxes(client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes, screenerName, junkName string, required map[string]bool) error {
	mailboxes, err := client.GetMailboxes()
	if err != nil {
		return fmt.Errorf("refresh mailboxes: %w", err)
	}
	updated, err := resolveWithIdentity(database, mailboxes, screenerName, junkName)
	if err != nil {
		return fmt.Errorf("resolve refreshed mailboxes: %w", err)
	}
	for id := range required {
		if _, ok := updated.Index.ByID[id]; !ok {
			return fmt.Errorf("mailbox %q still missing after refresh", id)
		}
	}
	// Counters are intentionally absent from the index. Persist dirty before
	// accepting metadata so a failed upload remains retryable on later cycles.
	if !reflect.DeepEqual(resolved.Index.ByID, updated.Index.ByID) || resolved.Screener.ID != updated.Screener.ID || !reflect.DeepEqual(resolved.Junk, updated.Junk) || !reflect.DeepEqual(resolved.Trash, updated.Trash) {
		if err := database.SetBoolState(sieveDirtyKey, true); err != nil {
			return err
		}
	}
	if err := saveMailboxIdentities(database, updated, screenerName, junkName); err != nil {
		return err
	}
	*resolved = *updated
	return nil
}

func BuildMailboxIndex(mailboxes []jmap.Mailbox) (*MailboxIndex, error) {
	raw := make(map[string]jmap.Mailbox, len(mailboxes))
	for _, mailbox := range mailboxes {
		raw[mailbox.ID] = mailbox
	}

	idx := &MailboxIndex{
		ByID:          make(map[string]MailboxInfo, len(mailboxes)),
		byName:        make(map[string][]MailboxInfo),
		byDisplayPath: make(map[string]MailboxInfo),
		byRole:        make(map[string][]MailboxInfo),
	}
	for _, mailbox := range mailboxes {
		names, err := mailboxNameChain(mailbox.ID, raw, nil)
		if err != nil {
			return nil, err
		}
		displayPath := strings.Join(names, "/")
		sievePath := buildMailboxSievePath(names)
		info := MailboxInfo{
			ID:          mailbox.ID,
			Name:        mailbox.Name,
			ParentID:    deref(mailbox.ParentID),
			DisplayPath: displayPath,
			SievePath:   sievePath,
		}
		if mailbox.Role != nil {
			info.Role = strings.ToLower(*mailbox.Role)
		}
		idx.ByID[info.ID] = info
		idx.byName[info.Name] = append(idx.byName[info.Name], info)
		idx.byDisplayPath[info.DisplayPath] = info
		if info.Role != "" {
			idx.byRole[info.Role] = append(idx.byRole[info.Role], info)
		}
	}
	return idx, nil
}

func ResolveMailboxes(mailboxes []jmap.Mailbox, screenerName, junkName string) (*ResolvedMailboxes, error) {
	idx, err := BuildMailboxIndex(mailboxes)
	if err != nil {
		return nil, err
	}
	screener, err := idx.resolveConfigured(screenerName)
	if err != nil {
		return nil, fmt.Errorf("resolve screener mailbox %q: %w", screenerName, err)
	}
	junk, err := idx.resolveRoleOrConfigured("junk", junkName)
	if err != nil {
		return nil, fmt.Errorf("resolve junk mailbox %q: %w", junkName, err)
	}
	trash, err := idx.resolveRoleOrConfigured("trash", "Trash")
	if err != nil {
		return nil, fmt.Errorf("resolve trash mailbox: %w", err)
	}
	return &ResolvedMailboxes{
		Index:        idx,
		Screener:     screener,
		Junk:         junk,
		Trash:        trash,
		ScreenerPath: screener.SievePath,
	}, nil
}

func (idx *MailboxIndex) resolveConfigured(value string) (MailboxInfo, error) {
	if info, ok := idx.byDisplayPath[value]; ok {
		return info, nil
	}
	matches := idx.byName[value]
	switch len(matches) {
	case 0:
		return MailboxInfo{}, fmt.Errorf("not found")
	case 1:
		return matches[0], nil
	default:
		return MailboxInfo{}, fmt.Errorf("name is ambiguous; use full display path")
	}
}

func (idx *MailboxIndex) resolveRoleOrConfigured(role, configuredName string) (*MailboxInfo, error) {
	if matches := idx.byRole[role]; len(matches) > 0 {
		if len(matches) > 1 {
			return nil, fmt.Errorf("multiple mailboxes have role %q", role)
		}
		info := matches[0]
		return &info, nil
	}
	if configuredName == "" {
		return nil, nil
	}
	info, err := idx.resolveConfigured(configuredName)
	if err != nil {
		if strings.Contains(err.Error(), "ambiguous") {
			return nil, err
		}
		return nil, nil
	}
	return &info, nil
}

func mailboxNameChain(id string, mailboxes map[string]jmap.Mailbox, seen map[string]bool) ([]string, error) {
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[id] {
		return nil, fmt.Errorf("mailbox parent cycle at %s", id)
	}
	mailbox, ok := mailboxes[id]
	if !ok {
		return nil, fmt.Errorf("mailbox %s missing from index", id)
	}
	seen[id] = true
	if mailbox.ParentID == nil || *mailbox.ParentID == "" {
		return []string{mailbox.Name}, nil
	}
	parent, ok := mailboxes[*mailbox.ParentID]
	if !ok {
		return nil, fmt.Errorf("mailbox %s references missing parent %s", id, *mailbox.ParentID)
	}
	names, err := mailboxNameChain(parent.ID, mailboxes, seen)
	if err != nil {
		return nil, err
	}
	return append(names, mailbox.Name), nil
}

func stripInboxRoot(names []string) []string {
	if len(names) > 1 && strings.EqualFold(names[0], "INBOX") {
		return names[1:]
	}
	return names
}

func buildMailboxSievePath(names []string) string {
	return sieve.BuildSievePath(strings.Join(names, "."))
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
