// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"fmt"
	"strings"

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

func refreshUnknownMailboxes(client *jmap.Client, resolved *ResolvedMailboxes, emails []jmap.Email, screenerName, junkName string) error {
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
	mailboxes, err := client.GetMailboxes()
	if err != nil {
		return fmt.Errorf("refresh mailboxes: %w", err)
	}
	updated, err := ResolveMailboxes(mailboxes, screenerName, junkName)
	if err != nil {
		return fmt.Errorf("resolve refreshed mailboxes: %w", err)
	}
	for id := range unknown {
		if _, ok := updated.Index.ByID[id]; !ok {
			return fmt.Errorf("mailbox %q still missing after refresh", id)
		}
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
