// SPDX-License-Identifier: Apache-2.0

package jmap

// === Top-level request/response envelope ===

// Request is the outer JMAP request object (RFC 8620 Section 3.3)
type Request struct {
	Using       []string     `json:"using"`
	MethodCalls []MethodCall `json:"methodCalls"`
}

// MethodCall is a single method invocation: [methodName, args, callID]
type MethodCall []interface{}

// Response is the outer JMAP response object
type Response struct {
	MethodResponses []MethodResponse `json:"methodResponses"`
	SessionState    string           `json:"sessionState,omitempty"`
}

// MethodResponse is: [methodName, result, callID]
type MethodResponse []interface{}

// === Mailbox/get ===

type MailboxGetArgs struct {
	AccountID string   `json:"accountId"`
	IDs       []string `json:"ids,omitempty"` // omit for all
}

type Mailbox struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ParentID      *string         `json:"parentId"`
	SortOrder     int             `json:"sortOrder"`
	TotalEmails   int             `json:"totalEmails"`
	UnreadEmails  int             `json:"unreadEmails"`
	TotalThreads  int             `json:"totalThreads"`
	UnreadThreads int             `json:"unreadThreads"`
	MyRights      map[string]bool `json:"myRights"`
	Role          *string         `json:"role"`
}

type MailboxGetResponse struct {
	AccountID string    `json:"accountId"`
	State     string    `json:"state"`
	List      []Mailbox `json:"list"`
	NotFound  []string  `json:"notFound,omitempty"`
}

// === Email/changes ===

type EmailChangesArgs struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges int    `json:"maxChanges,omitempty"`
}

type EmailChangesResponse struct {
	AccountID      string   `json:"accountId"`
	OldState       string   `json:"oldState"`
	NewState       string   `json:"newState"`
	HasMoreChanges bool     `json:"hasMoreChanges"`
	Changed        []string `json:"changed,omitempty"`
	Removed        []string `json:"removed,omitempty"`
	Created        []string `json:"created,omitempty"`
	Updated        []string `json:"updated,omitempty"`
	Destroyed      []string `json:"destroyed,omitempty"`
}

// === Email/get ===

type EmailGetArgs struct {
	AccountID  string   `json:"accountId"`
	IDs        []string `json:"ids"`
	Properties []string `json:"properties"`
}

type Emailer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Email struct {
	ID         string          `json:"id"`
	MailboxIDs map[string]bool `json:"mailboxIds"`
	From       []Emailer       `json:"from"`
	Sender     []Emailer       `json:"sender"`
	ReceivedAt string          `json:"receivedAt"`
}

type EmailGetResponse struct {
	AccountID string   `json:"accountId"`
	State     string   `json:"state"`
	List      []Email  `json:"list"`
	NotFound  []string `json:"notFound"`
}

// === Email/query ===

type EmailQueryArgs struct {
	AccountID      string      `json:"accountId"`
	Filter         interface{} `json:"filter,omitempty"`
	Sort           []SortSpec  `json:"sort,omitempty"`
	Position       int         `json:"position"`
	Limit          int         `json:"limit"`
	CalculateTotal bool        `json:"calculateTotal,omitempty"`
}

type SortSpec struct {
	Property    string `json:"property"`
	IsAscending bool   `json:"isAscending"`
}

type EmailQueryFilter struct {
	InMailbox string `json:"inMailbox,omitempty"`
}

type EmailQueryResponse struct {
	AccountID  string   `json:"accountId"`
	QueryState string   `json:"queryState"`
	IDs        []string `json:"ids"`
	Position   int      `json:"position"`
	Total      int      `json:"total"`
}

// === Email/set ===

type EmailSetArgs struct {
	IfInState string                `json:"ifInState,omitempty"`
	AccountID string                `json:"accountId"`
	Update    map[string]EmailPatch `json:"update,omitempty"`
}

type EmailPatch struct {
	MailboxIDs map[string]bool `json:"mailboxIds,omitempty"`
}

type EmailSetResponse struct {
	AccountID  string                 `json:"accountId"`
	Updated    map[string]interface{} `json:"updated,omitempty"`
	NotUpdated map[string]*SetError   `json:"notUpdated,omitempty"`
}

type SetError struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type MethodError struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// === SieveScript/set (RFC 9661) ===

type SieveScriptSetArgs struct {
	AccountID               string                      `json:"accountId"`
	Create                  map[string]SieveScriptInput `json:"create,omitempty"`
	Update                  map[string]SieveScriptInput `json:"update,omitempty"`
	OnSuccessActivateScript string                      `json:"onSuccessActivateScript,omitempty"`
}

type SieveScriptInput struct {
	Name   string `json:"name,omitempty"`
	BlobID string `json:"blobId,omitempty"`
}

type SieveScript struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsActive bool   `json:"isActive"`
	BlobID   string `json:"blobId"`
}

type SieveScriptSetResponse struct {
	AccountID  string                            `json:"accountId"`
	Created    map[string]map[string]interface{} `json:"created,omitempty"`
	Updated    map[string]interface{}            `json:"updated,omitempty"`
	NotCreated map[string]*SetError              `json:"notCreated,omitempty"`
	NotUpdated map[string]*SetError              `json:"notUpdated,omitempty"`
}

type BlobUploadResponse struct {
	AccountID string `json:"accountId"`
	BlobID    string `json:"blobId"`
	Type      string `json:"type"`
	Size      int    `json:"size"`
}

// === SieveScript/get ===

type SieveScriptGetArgs struct {
	AccountID string   `json:"accountId"`
	IDs       []string `json:"ids,omitempty"`
}

type SieveScriptGetResponse struct {
	AccountID string        `json:"accountId"`
	List      []SieveScript `json:"list"`
	NotFound  []string      `json:"notFound"`
}
