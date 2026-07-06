// SPDX-License-Identifier: Apache-2.0

package jmap

import "testing"

func TestParsePushMessage_StateChangeEmailForAccount(t *testing.T) {
	notify, requestErr, err := ParsePushMessage([]byte(`{
		"@type":"StateChange",
		"changed":{"acc1":{"Email":"s2"}}
	}`), "acc1")
	if err != nil {
		t.Fatalf("ParsePushMessage() error: %v", err)
	}
	if !notify || requestErr {
		t.Fatalf("ParsePushMessage() = notify %v requestErr %v, want true false", notify, requestErr)
	}
}

func TestParsePushMessage_IgnoresOtherAccount(t *testing.T) {
	notify, requestErr, err := ParsePushMessage([]byte(`{
		"@type":"StateChange",
		"changed":{"other":{"Email":"s2"}}
	}`), "acc1")
	if err != nil {
		t.Fatalf("ParsePushMessage() error: %v", err)
	}
	if notify || requestErr {
		t.Fatalf("ParsePushMessage() = notify %v requestErr %v, want false false", notify, requestErr)
	}
}

func TestParsePushMessage_IgnoresOtherDataType(t *testing.T) {
	notify, requestErr, err := ParsePushMessage([]byte(`{
		"@type":"StateChange",
		"changed":{"acc1":{"Calendar":"s2"}}
	}`), "acc1")
	if err != nil {
		t.Fatalf("ParsePushMessage() error: %v", err)
	}
	if notify || requestErr {
		t.Fatalf("ParsePushMessage() = notify %v requestErr %v, want false false", notify, requestErr)
	}
}

func TestParsePushMessage_StateChangeMailboxForAccount(t *testing.T) {
	notify, requestErr, err := ParsePushMessage([]byte(`{
		"@type":"StateChange",
		"changed":{"acc1":{"Mailbox":"s2"}}
	}`), "acc1")
	if err != nil {
		t.Fatalf("ParsePushMessage() error: %v", err)
	}
	if !notify || requestErr {
		t.Fatalf("ParsePushMessage() = notify %v requestErr %v, want true false", notify, requestErr)
	}
}

func TestParsePushMessage_RequestError(t *testing.T) {
	notify, requestErr, err := ParsePushMessage([]byte(`{"@type":"RequestError","type":"invalidArguments"}`), "acc1")
	if err != nil {
		t.Fatalf("ParsePushMessage() error: %v", err)
	}
	if notify || !requestErr {
		t.Fatalf("ParsePushMessage() = notify %v requestErr %v, want false true", notify, requestErr)
	}
}

func TestSendPushEventCoalesces(t *testing.T) {
	events := make(chan struct{}, 1)
	SendPushEvent(events)
	SendPushEvent(events)
	if got := len(events); got != 1 {
		t.Fatalf("len(events) = %d, want 1", got)
	}
}

func TestValidateWebSocketURL(t *testing.T) {
	if err := validateWebSocketURL("wss://mail.example.com/jmap/ws"); err != nil {
		t.Fatalf("validateWebSocketURL(wss) error: %v", err)
	}
	if err := validateWebSocketURL("ws://localhost/jmap/ws"); err != nil {
		t.Fatalf("validateWebSocketURL(localhost ws) error: %v", err)
	}
	if err := validateWebSocketURL("ws://mail.example.com/jmap/ws"); err == nil {
		t.Fatal("expected error for non-local ws URL")
	}
	if err := validateWebSocketURL("wss:mail.example.com/jmap/ws"); err == nil {
		t.Fatal("expected error for opaque wss URL")
	}
	if err := validateWebSocketURL("wss:///jmap/ws"); err == nil {
		t.Fatal("expected error for wss URL without host")
	}
}
