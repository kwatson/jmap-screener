// SPDX-License-Identifier: Apache-2.0

package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const pushReadLimit = 1 << 20

var pushEnableMessage = []byte(`{"@type":"WebSocketPushEnable","dataTypes":["Email","Mailbox"]}`)

type pushMessage struct {
	Type    string                       `json:"@type"`
	Changed map[string]map[string]string `json:"changed,omitempty"`
}

func WatchPush(ctx context.Context, wsURL, token, accountID string, events chan<- struct{}, connected, verified *atomic.Bool) {
	if err := validateWebSocketURL(wsURL); err != nil {
		slog.Warn("jmap push disabled", "error", err)
		return
	}

	backoff := 1 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := runPushConnection(ctx, wsURL, token, accountID, events, connected, verified)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("jmap push connection unavailable, polling fallback remains active", "error", err, "backoff", backoff)
		}
		if !waitContext(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func runPushConnection(ctx context.Context, wsURL, token, accountID string, events chan<- struct{}, connected, verified *atomic.Bool) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader:   header,
		HTTPClient:   noRedirectHTTPClient(),
		Subprotocols: []string{"jmap"},
	})
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "closing")
	conn.SetReadLimit(pushReadLimit)

	if conn.Subprotocol() != "jmap" {
		return fmt.Errorf("websocket subprotocol %q, want jmap", conn.Subprotocol())
	}

	if err := conn.Write(ctx, websocket.MessageText, pushEnableMessage); err != nil {
		return fmt.Errorf("enable push: %w", err)
	}
	if connected != nil {
		connected.Store(true)
		defer connected.Store(false)
	}
	SendPushEvent(events)
	slog.Info("jmap websocket push connected")

	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go keepalive(pingCtx, conn)

	for {
		messageType, reader, err := conn.Reader(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure {
				return nil
			}
			return fmt.Errorf("read push: %w", err)
		}
		if messageType != websocket.MessageText {
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("read push message: %w", err)
		}
		notify, requestErr, err := ParsePushMessage(body, accountID)
		if err != nil {
			slog.Debug("ignoring malformed jmap push message", "error", err)
			continue
		}
		if requestErr {
			return fmt.Errorf("push enable rejected by server")
		}
		if notify {
			if verified != nil && verified.CompareAndSwap(false, true) {
				slog.Info("jmap websocket push verified")
			}
			slog.Info("jmap websocket push state change received")
			SendPushEvent(events)
		}
	}
}

func keepalive(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := conn.Ping(pingCtx); err != nil {
				cancel()
				_ = conn.Close(websocket.StatusGoingAway, "ping failed")
				return
			}
			cancel()
		}
	}
}

func ParsePushMessage(data []byte, accountID string) (notify bool, requestError bool, err error) {
	var msg pushMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return false, false, err
	}
	switch msg.Type {
	case "StateChange":
		if accountID == "" || msg.Changed == nil {
			return false, false, nil
		}
		changes := msg.Changed[accountID]
		if _, ok := changes["Email"]; ok {
			return true, false, nil
		}
		if _, ok := changes["Mailbox"]; ok {
			return true, false, nil
		}
		return false, false, nil
	case "RequestError":
		return false, true, nil
	default:
		return false, false, nil
	}
}

func SendPushEvent(events chan<- struct{}) {
	select {
	case events <- struct{}{}:
	default:
	}
}

func validateWebSocketURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse websocket url: %w", err)
	}
	if parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("websocket url must be absolute")
	}
	if parsed.Scheme == "wss" {
		return nil
	}
	if parsed.Scheme == "ws" && isLocalhost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("websocket url must use wss")
}

func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: sameOriginRedirectOnly,
	}
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func waitContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
