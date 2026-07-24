// SPDX-License-Identifier: Apache-2.0

package jmap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// JMAP capabilities
	CapCore  = "urn:ietf:params:jmap:core"
	CapMail  = "urn:ietf:params:jmap:mail"
	CapSieve = "urn:ietf:params:jmap:sieve"
)

// Client is a JMAP API client connected to a single user account.
type Client struct {
	URL                    string
	Token                  string
	AccountID              string
	http                   *http.Client
	uploadURLTemplate      string
	uploadURLTemplateErr   error
	advertisedHostOverride string
}

func (c *Client) SetAdvertisedHostOverride(host string) error {
	normalized, err := normalizeAdvertisedHostname(host)
	if err != nil {
		return err
	}
	c.advertisedHostOverride = normalized
	return nil
}

func (c *Client) RewriteAdvertisedURL(rawURL string) (string, error) {
	return rewriteURLHostname(rawURL, c.advertisedHostOverride)
}

// NewClient creates a new JMAP client.
func NewClient(url, token, accountID string) *Client {
	return &Client{
		URL:       url,
		Token:     token,
		AccountID: accountID,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) SetUploadURLTemplate(template string) error {
	if _, err := ResolveUploadURL(template, c.AccountID); err != nil {
		c.uploadURLTemplateErr = err
		return err
	}
	c.uploadURLTemplate = template
	c.uploadURLTemplateErr = nil
	return nil
}

// do sends a JMAP request and decodes the response.
// callID is the identifier for the method call (used to find the response).
func (c *Client) do(req *Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &AuthError{StatusCode: resp.StatusCode, Body: string(respBody)}
		}
		return nil, fmt.Errorf("jmap returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var jmapResp Response
	if err := json.Unmarshal(respBody, &jmapResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &jmapResp, nil
}

// --- Mailbox/get ---

// GetMailboxes fetches all mailboxes for the account.
func (c *Client) GetMailboxes() ([]Mailbox, error) {
	req := &Request{
		Using: []string{CapCore, CapMail},
		MethodCalls: []MethodCall{
			{"Mailbox/get", &MailboxGetArgs{AccountID: c.AccountID}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}

	// We need to extract the first method response.
	// The MethodResponses are raw []interface{} - need manual extraction.
	result, err := extractMailboxGetResponse(resp)
	if err != nil {
		return nil, err
	}
	return result.List, nil
}

// --- Email/changes ---

// EmailChanges polls for changes since the given state.
func (c *Client) EmailChanges(sinceState string, maxChanges int) (*EmailChangesResponse, error) {
	if maxChanges <= 0 {
		maxChanges = 500
	}
	req := &Request{
		Using: []string{CapCore, CapMail},
		MethodCalls: []MethodCall{
			{"Email/changes", &EmailChangesArgs{
				AccountID:  c.AccountID,
				SinceState: sinceState,
				MaxChanges: maxChanges,
			}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return extractEmailChangesResponse(resp)
}

// --- Email/get ---

// GetEmail fetches a single email by ID with specified properties.
func (c *Client) GetEmail(id string, properties ...string) (*Email, error) {
	if len(properties) == 0 {
		properties = []string{"mailboxIds", "from", "sender", "receivedAt"}
	}
	emails, err := c.GetEmails([]string{id}, properties...)
	if err != nil {
		return nil, err
	}
	if len(emails) == 0 {
		return nil, fmt.Errorf("email %s not found", id)
	}
	return &emails[0], nil
}

// GetEmails fetches multiple emails by IDs with specified properties.
func (c *Client) GetEmails(ids []string, properties ...string) ([]Email, error) {
	result, err := c.GetEmailsWithState(ids, properties...)
	if err != nil {
		return nil, err
	}
	return result.List, nil
}

func (c *Client) GetEmailsWithState(ids []string, properties ...string) (*EmailGetResponse, error) {
	if len(properties) == 0 {
		properties = []string{"mailboxIds", "from", "sender", "receivedAt"}
	}
	req := &Request{
		Using: []string{CapCore, CapMail},
		MethodCalls: []MethodCall{
			{"Email/get", &EmailGetArgs{
				AccountID:  c.AccountID,
				IDs:        ids,
				Properties: properties,
			}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return extractEmailGetResponse(resp)
}

// --- Email/query ---

// QueryEmails queries for emails in a specific mailbox, returning paginated IDs.
// Returns (ids, total, position, error). Caller must paginate by incrementing position.
func (c *Client) QueryEmails(inMailboxID string, position, limit int) ([]string, int, int, error) {
	if limit <= 0 {
		limit = 100
	}
	req := &Request{
		Using: []string{CapCore, CapMail},
		MethodCalls: []MethodCall{
			{"Email/query", &EmailQueryArgs{
				AccountID:      c.AccountID,
				Filter:         EmailQueryFilter{InMailbox: inMailboxID},
				Sort:           []SortSpec{{Property: "receivedAt", IsAscending: false}},
				Position:       position,
				Limit:          limit,
				CalculateTotal: true,
			}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	result, err := extractEmailQueryResponse(resp)
	if err != nil {
		return nil, 0, 0, err
	}
	return result.IDs, result.Total, result.Position, nil
}

// --- Email/set ---

// MoveEmail moves an email to the target mailbox by setting its mailboxIds.
func (c *Client) MoveEmail(emailID, targetMailboxID string) (*SetError, error) {
	req := &Request{
		Using: []string{CapCore, CapMail},
		MethodCalls: []MethodCall{
			{"Email/set", &EmailSetArgs{
				AccountID: c.AccountID,
				Update: map[string]EmailPatch{
					emailID: {
						MailboxIDs: map[string]bool{targetMailboxID: true},
					},
				},
			}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	result, err := extractEmailSetResponse(resp)
	if err != nil {
		return nil, err
	}
	if setErr, ok := result.NotUpdated[emailID]; ok {
		return setErr, nil
	}
	if _, ok := result.Updated[emailID]; !ok {
		return nil, fmt.Errorf("email/set response missing updated or notUpdated for %s", emailID)
	}
	return nil, nil // success
}

// --- SieveScript/get ---

// GetSieveScripts fetches all Sieve scripts for the account.
func (c *Client) GetSieveScripts() ([]SieveScript, error) {
	req := &Request{
		Using: []string{CapCore, CapSieve},
		MethodCalls: []MethodCall{
			{"SieveScript/get", &SieveScriptGetArgs{AccountID: c.AccountID}, "c1"},
		},
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	result, err := extractSieveScriptGetResponse(resp)
	if err != nil {
		return nil, err
	}
	return result.List, nil
}

// --- SieveScript/set ---

// CreateSieveScript creates a new Sieve script.
func (c *Client) CreateSieveScript(name, contents string, isActive bool) error {
	blobID, err := c.uploadSieveScript(contents)
	if err != nil {
		return err
	}
	createID := "script"
	args := &SieveScriptSetArgs{
		AccountID: c.AccountID,
		Create: map[string]SieveScriptInput{
			createID: {
				Name:   name,
				BlobID: blobID,
			},
		},
	}
	if isActive {
		args.OnSuccessActivateScript = "#" + createID
	}
	req := &Request{
		Using: []string{CapCore, CapSieve},
		MethodCalls: []MethodCall{
			{"SieveScript/set", args, "c1"},
		},
	}
	return c.sieveSetRequest(req, createID, true)
}

// UpdateSieveScript updates an existing Sieve script by server script ID.
func (c *Client) UpdateSieveScript(id, contents string, isActive bool) error {
	blobID, err := c.uploadSieveScript(contents)
	if err != nil {
		return err
	}
	args := &SieveScriptSetArgs{
		AccountID: c.AccountID,
		Update: map[string]SieveScriptInput{
			id: {
				BlobID: blobID,
			},
		},
	}
	if isActive {
		args.OnSuccessActivateScript = id
	}
	req := &Request{
		Using: []string{CapCore, CapSieve},
		MethodCalls: []MethodCall{
			{"SieveScript/set", args, "c1"},
		},
	}
	return c.sieveSetRequest(req, id, false)
}

func (c *Client) uploadSieveScript(contents string) (string, error) {
	uploadURL, err := c.uploadURL()
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequest("POST", uploadURL, strings.NewReader(contents))
	if err != nil {
		return "", fmt.Errorf("create sieve upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/sieve")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	uploadHTTPClient := *c.http
	uploadHTTPClient.CheckRedirect = sameOriginRedirectOnly
	resp, err := uploadHTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sieve upload request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read sieve upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("sieve upload returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var uploadResp BlobUploadResponse
	if err := json.Unmarshal(respBody, &uploadResp); err != nil {
		return "", fmt.Errorf("unmarshal sieve upload response: %w", err)
	}
	if uploadResp.BlobID == "" {
		return "", fmt.Errorf("sieve upload response missing blobId")
	}
	return uploadResp.BlobID, nil
}

func (c *Client) uploadURL() (string, error) {
	if c.advertisedHostOverride != "" && c.uploadURLTemplateErr != nil {
		return "", fmt.Errorf("resolve advertised upload URL: %w", c.uploadURLTemplateErr)
	}
	if c.uploadURLTemplate != "" {
		uploadURL, err := ResolveUploadURL(c.uploadURLTemplate, c.AccountID)
		if err == nil {
			uploadURL, err = c.RewriteAdvertisedURL(uploadURL)
			if err == nil {
				err = validateUploadURL(uploadURL)
			}
		}
		if err == nil {
			return uploadURL, nil
		}
		if c.advertisedHostOverride != "" {
			return "", fmt.Errorf("resolve advertised upload URL: %w", err)
		}
	}

	parsed, err := url.Parse(c.URL)
	if err != nil {
		return "", fmt.Errorf("parse jmap url: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/upload/" + url.PathEscape(c.AccountID) + "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (c *Client) sieveSetRequest(req *Request, id string, create bool) error {
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	result, err := extractSieveScriptSetResponse(resp)
	if err != nil {
		return err
	}
	for key, setErr := range result.NotCreated {
		return fmt.Errorf("sieve script create rejected for %s: %s - %s", key, setErr.Type, setErr.Description)
	}
	for key, setErr := range result.NotUpdated {
		return fmt.Errorf("sieve script update rejected for %s: %s - %s", key, setErr.Type, setErr.Description)
	}
	if create {
		if _, ok := result.Created[id]; !ok {
			return fmt.Errorf("sieve script create response missing created or notCreated for %s", id)
		}
		return nil
	}
	if _, ok := result.Updated[id]; !ok {
		return fmt.Errorf("sieve script update response missing updated or notUpdated for %s", id)
	}
	return nil
}

// --- Response extraction helpers ---

// AuthError indicates an HTTP 401 or 403 response.
type AuthError struct {
	StatusCode int
	Body       string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("jmap authentication failed (HTTP %d): %s", e.StatusCode, e.Body)
}

func (e *MethodError) Error() string {
	if e.Description == "" {
		return fmt.Sprintf("jmap method error: %s", e.Type)
	}
	return fmt.Sprintf("jmap method error: %s: %s", e.Type, e.Description)
}

func IsInvalidEmailState(err error) bool {
	var methodErr *MethodError
	if !errors.As(err, &methodErr) {
		return false
	}
	switch methodErr.Type {
	case "cannotCalculateChanges", "invalidArguments":
		return true
	default:
		return false
	}
}

// Helper: extract the map result from a MethodResponse
// MethodResponse is [methodName string, result map, callID string]
// We unmarshal from JSON and extract, then re-marshal the result to our target type.

func extractResult(resp *Response, expectedMethod, expectedCallID string) (map[string]interface{}, error) {
	if len(resp.MethodResponses) == 0 {
		return nil, fmt.Errorf("no method responses")
	}
	mr := resp.MethodResponses[0]
	if len(mr) != 3 {
		return nil, fmt.Errorf("invalid method response format")
	}
	method, ok := mr[0].(string)
	if !ok {
		return nil, fmt.Errorf("method response name is not a string")
	}
	if method != expectedMethod && method != "error" {
		return nil, fmt.Errorf("unexpected method response %q, want %q", method, expectedMethod)
	}
	callID, ok := mr[2].(string)
	if !ok {
		return nil, fmt.Errorf("method response call id is not a string")
	}
	if callID != expectedCallID {
		return nil, fmt.Errorf("unexpected method response call id %q, want %q", callID, expectedCallID)
	}
	result, ok := mr[1].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("method result is not a map")
	}
	if method == "error" {
		methodErr, err := decodeInto[MethodError](result)
		if err != nil {
			return nil, err
		}
		return nil, methodErr
	}
	return result, nil
}

func extractMailboxGetResponse(resp *Response) (*MailboxGetResponse, error) {
	result, err := extractResult(resp, "Mailbox/get", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[MailboxGetResponse](result)
}

func extractEmailChangesResponse(resp *Response) (*EmailChangesResponse, error) {
	result, err := extractResult(resp, "Email/changes", "c1")
	if err != nil {
		return nil, err
	}
	changes, err := decodeInto[EmailChangesResponse](result)
	if err != nil {
		return nil, err
	}
	changes.Changed = appendUniqueStrings(changes.Changed, changes.Created...)
	changes.Changed = appendUniqueStrings(changes.Changed, changes.Updated...)
	changes.Removed = appendUniqueStrings(changes.Removed, changes.Destroyed...)
	return changes, nil
}

func appendUniqueStrings(dst []string, src ...string) []string {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]bool, len(dst)+len(src))
	for _, value := range dst {
		seen[value] = true
	}
	for _, value := range src {
		if !seen[value] {
			dst = append(dst, value)
			seen[value] = true
		}
	}
	return dst
}

func extractEmailGetResponse(resp *Response) (*EmailGetResponse, error) {
	result, err := extractResult(resp, "Email/get", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[EmailGetResponse](result)
}

func extractEmailQueryResponse(resp *Response) (*EmailQueryResponse, error) {
	result, err := extractResult(resp, "Email/query", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[EmailQueryResponse](result)
}

func extractEmailSetResponse(resp *Response) (*EmailSetResponse, error) {
	result, err := extractResult(resp, "Email/set", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[EmailSetResponse](result)
}

func extractSieveScriptGetResponse(resp *Response) (*SieveScriptGetResponse, error) {
	result, err := extractResult(resp, "SieveScript/get", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[SieveScriptGetResponse](result)
}

func extractSieveScriptSetResponse(resp *Response) (*SieveScriptSetResponse, error) {
	result, err := extractResult(resp, "SieveScript/set", "c1")
	if err != nil {
		return nil, err
	}
	return decodeInto[SieveScriptSetResponse](result)
}

// decodeInto is a helper that JSON round-trips a map into a typed struct.
func decodeInto[T any](m map[string]interface{}) (*T, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("remarshal: %w", err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal into %T: %w", result, err)
	}
	return &result, nil
}
