// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kwatson/jmap-screener/internal/config"
	"github.com/kwatson/jmap-screener/internal/db"
	"github.com/kwatson/jmap-screener/internal/jmap"
	"github.com/kwatson/jmap-screener/internal/sieve"
)

const (
	stateKey                   = "jmap_state"
	sieveDirtyKey              = "sieve_dirty"
	lastSieveErrorKey          = "last_sieve_error"
	maxChanges                 = 500
	scriptName                 = "screener"
	unverifiedPushPollInterval = 30 * time.Second
)

// Run starts the main monitor loop.
func Run(cfg *config.Config) error {
	// Setup structured logging
	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Create JMAP client and validate endpoint overrides before opening local resources.
	client := jmap.NewClient(cfg.JMAP.URL, cfg.JMAP.Token, cfg.JMAP.AccountID)
	if err := client.SetAdvertisedHostOverride(cfg.JMAP.AdvertisedHostOverride); err != nil {
		return fmt.Errorf("configure jmap advertised host override: %w", err)
	}
	if cfg.JMAP.AdvertisedHostOverride != "" {
		slog.Info("jmap advertised endpoint host override enabled", "host", cfg.JMAP.AdvertisedHostOverride)
	}

	// Open database
	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	healthy, err := startHealthServer(cfg.Health.Port)
	if err != nil {
		return err
	}

	session := discoverSessionForStartup(client, cfg.JMAP.AccountID)
	if client.AccountID == "" {
		return fmt.Errorf("jmap.account_id is not configured and session discovery did not return a primary mail account")
	}
	pushEvents := make(chan struct{}, 1)
	var pushConnected atomic.Bool
	var pushVerified atomic.Bool
	pushCtx, cancelPush := context.WithCancel(context.Background())
	defer cancelPush()
	if cfg.JMAP.PushEnabled {
		startPushWatcher(pushCtx, client, client.AccountID, session, pushEvents, &pushConnected, &pushVerified)
	} else {
		slog.Info("jmap websocket push disabled by config")
	}

	// Discover mailboxes
	mailboxes, err := client.GetMailboxes()
	if err != nil {
		return fmt.Errorf("discover mailboxes: %w", err)
	}

	resolved, err := ResolveMailboxes(mailboxes, cfg.Monitor.ScreenerMailbox, cfg.Monitor.JunkMailbox)
	if err != nil {
		return fmt.Errorf("resolve mailboxes: %w", err)
	}
	screenerID := resolved.Screener.ID
	junkID := ""
	if resolved.Junk != nil {
		junkID = resolved.Junk.ID
	} else {
		slog.Warn("junk mailbox not found, spam routing and rescue disabled")
	}

	slog.Info("mailbox discovery complete",
		"screener_id", screenerID,
		"junk_id", junkID,
		"total_mailboxes", len(mailboxes),
	)

	// Load saved state
	savedState, err := database.GetState(stateKey)
	if err != nil {
		slog.Warn("failed to load saved state, treating as first run", "error", err)
	}
	state := savedState

	// Startup: regenerate Sieve script from DB
	if err := uploadSieveScript(client, database, resolved, cfg.Monitor.SpamScoreThreshold); err != nil {
		slog.Error("startup sieve upload failed", "error", err)
		_ = database.SetBoolState(sieveDirtyKey, true)
		_ = database.SetState(lastSieveErrorKey, err.Error())
	} else {
		_ = database.SetBoolState(sieveDirtyKey, false)
		_ = database.SetState(lastSieveErrorKey, "")
	}

	if state == "" {
		bootstrapState, err := bootstrapCache(client, database, resolved)
		if err != nil {
			healthy.Store(false)
			return fmt.Errorf("bootstrap email cache: %w", err)
		}
		state = bootstrapState
		if state != "" {
			if err := saveState(database, state); err != nil {
				slog.Error("save bootstrap state", "error", err)
			}
		}
		slog.Info("startup cache bootstrap complete", "email_state_saved", state != "")
	}
	updateHealth(database, healthy, true)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// Main loop
	rescuedByMe := make(map[string]bool)
	backoff := 1 * time.Second
	pollInterval := cfg.Daemon.PollInterval()
	shutdown := func(sig os.Signal) error {
		slog.Info("shutting down", "signal", sig)
		cancelPush()
		healthy.Store(false)
		if err := saveState(database, state); err != nil {
			slog.Error("save state on shutdown", "error", err)
		}
		return nil
	}

	for {
		select {
		case sig := <-sigCh:
			return shutdown(sig)
		default:
		}
		drainPushEvents(pushEvents)

		var err error
		state, err = runMonitorCycle(cfg, client, database, resolved, rescuedByMe, state, healthy)
		if err != nil {
			var authErr *jmap.AuthError
			if errors.As(err, &authErr) {
				slog.Error("jmap authentication failed, exiting", "status", authErr.StatusCode)
				return fmt.Errorf("jmap auth: %w", err)
			}
			slog.Error("email/changes failed", "error", err, "backoff", backoff)
			healthy.Store(false)
		} else {
			backoff = 1 * time.Second
		}

		wait := pollInterval
		if err != nil {
			wait = backoff
			backoff *= 2
			if backoff > pollInterval {
				backoff = pollInterval
			}
		} else if cfg.JMAP.PushEnabled && (!pushConnected.Load() || !pushVerified.Load()) && wait > unverifiedPushPollInterval {
			wait = unverifiedPushPollInterval
		}
		if sig, ok := waitForWake(sigCh, pushEvents, wait); ok {
			return shutdown(sig)
		}
	}
}

// --- Helper functions ---

func discoverSessionForStartup(client *jmap.Client, configuredAccountID string) *jmap.Session {
	session, err := client.DiscoverSession()
	if err != nil {
		slog.Warn("jmap session discovery failed, using inferred upload URL and polling fallback if possible", "error", err)
		return nil
	}
	if discovered := session.MailPrimaryAccountID(); discovered != "" {
		if configuredAccountID == "" {
			client.AccountID = discovered
			slog.Info("using discovered primary mail account", "account_id", discovered)
		} else if discovered != configuredAccountID {
			slog.Warn("jmap session primary mail account differs from configured account_id", "configured_account_id", configuredAccountID, "discovered_account_id", discovered)
		}
	}
	if session.UploadURL != "" {
		if err := client.SetUploadURLTemplate(session.UploadURL); err != nil {
			slog.Warn("jmap session uploadUrl invalid", "error", err)
		}
	}
	return session
}

func startPushWatcher(ctx context.Context, client *jmap.Client, configuredAccountID string, session *jmap.Session, events chan<- struct{}, connected, verified *atomic.Bool) {
	if session == nil {
		return
	}
	capability, ok, err := session.WebSocketCapability()
	if err != nil {
		slog.Warn("jmap websocket capability could not be parsed, polling fallback remains active", "error", err)
		return
	}
	if !ok || capability == nil || !capability.SupportsPush || capability.URL == "" {
		slog.Info("jmap websocket push unavailable, polling fallback remains active")
		return
	}
	wsURL, err := client.RewriteAdvertisedURL(capability.URL)
	if err != nil {
		slog.Warn("jmap websocket endpoint override failed, polling fallback remains active", "error", err)
		return
	}
	if err := jmap.ValidateWebSocketURL(wsURL); err != nil {
		slog.Warn("jmap websocket endpoint invalid, polling fallback remains active", "error", err)
		return
	}
	go jmap.WatchPush(ctx, wsURL, client.Token, configuredAccountID, events, connected, verified)
}

func updateHealth(database *db.DB, healthy *atomic.Bool, jmapHealthy bool) {
	if !jmapHealthy {
		healthy.Store(false)
		return
	}
	dirty, err := database.GetBoolState(sieveDirtyKey)
	if err != nil || dirty {
		healthy.Store(false)
		return
	}
	healthy.Store(true)
}

func runMonitorCycle(cfg *config.Config, client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes, rescuedByMe map[string]bool, state string, healthy *atomic.Bool) (string, error) {
	needsReload := false
	currentState := state

	if state == "" {
		bootstrapState, err := bootstrapCache(client, database, resolved)
		if err != nil {
			slog.Error("bootstrap email cache failed", "error", err)
			healthy.Store(false)
		} else if bootstrapState != "" {
			state = bootstrapState
			currentState = bootstrapState
			if err := saveState(database, state); err != nil {
				slog.Error("save bootstrap state", "error", err)
			}
			updateHealth(database, healthy, true)
		}
	}

	// Phase 1: Poll for changes (with paging for hasMoreChanges)
	changesComplete := true
	if state != "" {
		hasMore := true
		for hasMore {
			changes, err := client.EmailChanges(currentState, maxChanges)
			if err != nil {
				if jmap.IsInvalidEmailState(err) {
					slog.Warn("email state expired; rebuilding cache", "error", err)
					bootstrapState, bootstrapErr := bootstrapCache(client, database, resolved)
					if bootstrapErr != nil {
						slog.Error("resync cache rebuild failed", "error", bootstrapErr)
						healthy.Store(false)
					} else {
						state = bootstrapState
						currentState = bootstrapState
						if saveErr := saveState(database, bootstrapState); saveErr != nil {
							slog.Error("save rebuilt email state", "error", saveErr)
						}
						updateHealth(database, healthy, true)
					}
					changesComplete = false
					break
				}
				return state, err
			}
			updateHealth(database, healthy, true)
			if len(changes.Changed) > 0 || len(changes.Removed) > 0 || changes.HasMoreChanges {
				slog.Info("jmap email changes received", "changed", len(changes.Changed), "removed", len(changes.Removed), "has_more", changes.HasMoreChanges)
			}

			// Batch fetch changed emails
			if len(changes.Changed) > 0 {
				result, err := client.GetEmailsWithState(changes.Changed, "mailboxIds", "from", "sender")
				if err != nil {
					slog.Error("email/get batch failed", "error", err)
					healthy.Store(false)
					changesComplete = false
					break
				}
				// Resolve newly created folders before consuming any moves in this batch.
				// A failed refresh must leave the email state and cached moves retryable.
				if err := refreshUnknownMailboxes(client, resolved, result.List, cfg.Monitor.ScreenerMailbox, cfg.Monitor.JunkMailbox); err != nil {
					return state, err
				}
				for _, emailID := range result.NotFound {
					slog.Info("changed email not found; deleting cache", "email", emailID)
					if err := database.DeleteEmailCache(emailID); err != nil {
						slog.Error("delete email cache", "error", err)
						changesComplete = false
						break
					}
				}
				if !changesComplete {
					break
				}
				emailByID := make(map[string]jmap.Email)
				for _, e := range result.List {
					emailByID[e.ID] = e
				}

				for _, emailID := range changes.Changed {
					// Skip self-inflicted moves (sentry)
					if rescuedByMe[emailID] {
						delete(rescuedByMe, emailID)
						continue
					}

					email, ok := emailByID[emailID]
					if !ok {
						continue // not found in response
					}

					routeChanged, err := processEmailChange(database, resolved, email, cfg.Monitor.ExcludedSenders)
					if err != nil {
						slog.Error("process email change", "error", err, "email", emailID)
						changesComplete = false
						break
					}
					if routeChanged {
						needsReload = true
					}
				}
			}

			if !changesComplete {
				break
			}

			// Handle removed emails (clean cache)
			for _, emailID := range changes.Removed {
				if err := database.DeleteEmailCache(emailID); err != nil {
					slog.Error("delete email cache", "error", err)
					changesComplete = false
					break
				}
			}
			if !changesComplete {
				break
			}

			currentState = changes.NewState
			hasMore = changes.HasMoreChanges
		}
	}
	if changesComplete && currentState != "" {
		state = currentState
		if err := saveState(database, state); err != nil {
			slog.Error("save state", "error", err)
		}
	}

	// Phase 2: Rescue known senders from Junk
	if cfg.Rescue.Enabled && resolved.Junk != nil {
		if _, err := rescueJunkPage(client, database, resolved, rescuedByMe, cfg.Rescue.BatchSize); err != nil {
			slog.Error("junk rescue failed", "error", err)
			healthy.Store(false)
		}
	}

	// Phase 3: Update Sieve script if routes changed
	dirty, err := database.GetBoolState(sieveDirtyKey)
	if err != nil {
		slog.Error("load sieve dirty state", "error", err)
	}
	if needsReload || dirty {
		if err := uploadSieveScript(client, database, resolved, cfg.Monitor.SpamScoreThreshold); err != nil {
			slog.Error("sieve script update failed", "error", err)
			_ = database.SetBoolState(sieveDirtyKey, true)
			_ = database.SetState(lastSieveErrorKey, err.Error())
			healthy.Store(false)
		} else {
			_ = database.SetBoolState(sieveDirtyKey, false)
			_ = database.SetState(lastSieveErrorKey, "")
			updateHealth(database, healthy, true)
		}
	}

	return state, nil
}

func startHealthServer(port int) (*atomic.Bool, error) {
	var healthy atomic.Bool
	healthy.Store(false)
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("start health server on %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unhealthy"))
		}
	})
	go func() {
		slog.Info("health check listening", "addr", addr)
		if err := http.Serve(ln, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health server error", "error", err)
			healthy.Store(false)
		}
	}()
	return &healthy, nil
}

func uploadSieveScript(client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes, spamScoreThreshold int) error {
	routes, err := database.GetAllRoutes()
	if err != nil {
		return fmt.Errorf("get routes: %w", err)
	}

	var sieveRoutes []sieve.Route
	for _, r := range routes {
		target, ok := resolved.Index.ByID[r.TargetMailboxID]
		if !ok {
			slog.Warn("skipping route with missing target mailbox", "sender", r.SenderEmail, "target_mailbox_id", r.TargetMailboxID)
			continue
		}
		sieveRoutes = append(sieveRoutes, sieve.Route{
			SenderEmail:   r.SenderEmail,
			TargetMailbox: r.TargetMailbox,
			TargetPath:    target.SievePath,
		})
	}

	var spamRule *sieve.SpamRule
	if resolved.Junk != nil {
		spamRule = &sieve.SpamRule{JunkPath: resolved.Junk.SievePath, Threshold: spamScoreThreshold}
	}
	script := sieve.Generate(sieveRoutes, resolved.ScreenerPath, spamRule)

	// Check if screener script already exists
	scripts, err := client.GetSieveScripts()
	if err != nil {
		slog.Warn("sieve/get failed, attempting create", "error", err)
		return client.CreateSieveScript(scriptName, script, true)
	}

	var existingID string
	for _, s := range scripts {
		if s.Name == scriptName {
			existingID = s.ID
			break
		}
	}

	if existingID != "" {
		slog.Info("updating sieve script", "routes", len(routes))
		return client.UpdateSieveScript(existingID, script, true)
	}

	slog.Info("creating sieve script", "routes", len(routes))
	return client.CreateSieveScript(scriptName, script, true)
}

func saveState(database *db.DB, state string) error {
	return database.SetState(stateKey, state)
}

func waitForWake(sigCh <-chan os.Signal, pushEvents <-chan struct{}, d time.Duration) (os.Signal, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case sig := <-sigCh:
		return sig, true
	case <-pushEvents:
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

func drainPushEvents(pushEvents <-chan struct{}) {
	for {
		select {
		case <-pushEvents:
		default:
			return
		}
	}
}

func bootstrapCache(client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes) (string, error) {
	baseline, err := client.GetEmailsWithState([]string{}, "id")
	if err != nil {
		return "", fmt.Errorf("get initial email state: %w", err)
	}
	if baseline.State == "" {
		return "", fmt.Errorf("get initial email state: empty state")
	}

	if err := cacheMailboxEmails(client, database, resolved.Screener.ID); err != nil {
		return "", err
	}
	if resolved.Junk != nil {
		if err := cacheMailboxEmails(client, database, resolved.Junk.ID); err != nil {
			return "", err
		}
	}

	state, err := reconcileEmailCache(client, database, baseline.State)
	if err != nil {
		return "", err
	}
	return state, nil
}

func cacheMailboxEmails(client *jmap.Client, database *db.DB, mailboxID string) error {
	position := 0
	limit := 100
	for {
		ids, total, responsePosition, err := client.QueryEmails(mailboxID, position, limit)
		if err != nil {
			return fmt.Errorf("query mailbox %s: %w", mailboxID, err)
		}
		if len(ids) == 0 {
			break
		}
		result, err := client.GetEmailsWithState(ids, "mailboxIds", "from", "sender")
		if err != nil {
			return fmt.Errorf("get mailbox emails %s: %w", mailboxID, err)
		}
		for _, email := range result.List {
			if err := database.UpsertEmailCache(email.ID, keys(email.MailboxIDs)); err != nil {
				return err
			}
		}
		position = responsePosition + len(ids)
		if queryPageComplete(position, total, len(ids), limit) {
			break
		}
	}
	return nil
}

func reconcileEmailCache(client *jmap.Client, database *db.DB, state string) (string, error) {
	currentState := state
	for {
		changes, err := client.EmailChanges(currentState, maxChanges)
		if err != nil {
			return "", fmt.Errorf("email/changes during bootstrap: %w", err)
		}

		if len(changes.Changed) > 0 {
			result, err := client.GetEmailsWithState(changes.Changed, "mailboxIds")
			if err != nil {
				return "", fmt.Errorf("email/get changed during bootstrap: %w", err)
			}
			for _, email := range result.List {
				if err := database.UpsertEmailCache(email.ID, keys(email.MailboxIDs)); err != nil {
					return "", err
				}
			}
			for _, emailID := range result.NotFound {
				if err := database.DeleteEmailCache(emailID); err != nil {
					return "", err
				}
			}
		}

		for _, emailID := range changes.Removed {
			if err := database.DeleteEmailCache(emailID); err != nil {
				return "", err
			}
		}

		currentState = changes.NewState
		if !changes.HasMoreChanges {
			return currentState, nil
		}
	}
}

func processEmailChange(database *db.DB, resolved *ResolvedMailboxes, email jmap.Email, excludedSenders []string) (bool, error) {
	prev, err := database.GetEmailCache(email.ID)
	if err != nil {
		return false, fmt.Errorf("get email cache: %w", err)
	}

	mailboxIDs := keys(email.MailboxIDs)
	if prev == nil {
		routeChanged := false
		if contains(mailboxIDs, resolved.Screener.ID) {
			slog.Info("cached new screener message", "email", email.ID, "sender", db.NormalizeSender(extractSender(email)))
			var err error
			routeChanged, err = resetSenderRoute(database, email, "reset sender route (found in screener)")
			if err != nil {
				return false, err
			}
		} else {
			slog.Info("cached new non-screener message", "email", email.ID, "mailbox_ids", mailboxIDs)
		}
		if err := database.UpsertEmailCache(email.ID, mailboxIDs); err != nil {
			return false, fmt.Errorf("update email cache: %w", err)
		}
		return routeChanged, nil
	}

	routeChanged, err := applyEmailTransition(database, resolved, email, prev.MailboxIDs, mailboxIDs, excludedSenders)
	if err != nil {
		return false, err
	}
	if err := database.UpsertEmailCache(email.ID, mailboxIDs); err != nil {
		return false, fmt.Errorf("update email cache: %w", err)
	}
	return routeChanged, nil
}

func applyEmailTransition(database *db.DB, resolved *ResolvedMailboxes, email jmap.Email, prevMailboxIDs, mailboxIDs []string, excludedSenders []string) (bool, error) {
	screenerID := resolved.Screener.ID
	movedIntoScreener := !contains(prevMailboxIDs, screenerID) && contains(mailboxIDs, screenerID)
	if movedIntoScreener {
		return resetSenderRoute(database, email, "reset sender route (moved to screener)")
	}

	// Was in Screener, now not: user moved it out.
	if contains(prevMailboxIDs, screenerID) && !contains(mailboxIDs, screenerID) {
		targetID := findNewMailbox(mailboxIDs, screenerID)
		if targetID == "" {
			slog.Info("skipping screener move without single target", "email", email.ID, "previous_mailbox_ids", prevMailboxIDs, "current_mailbox_ids", mailboxIDs)
			return false, nil
		}

		targetMbox, ok := resolved.Index.ByID[targetID]
		if !ok {
			slog.Warn("unknown target mailbox", "id", targetID)
			return false, nil
		}

		sender := db.NormalizeSender(extractSender(email))
		if sender == "" {
			slog.Info("skipping screener move without sender", "email", email.ID)
			return false, nil
		}

		if resolved.Trash != nil && targetMbox.ID == resolved.Trash.ID {
			deleted, err := deleteRouteIfExists(database, sender)
			if err != nil {
				return false, err
			}
			if deleted {
				slog.Info("blocked sender (trashed)", "sender", sender)
				_ = database.SetBoolState(sieveDirtyKey, true)
				return true, nil
			}
			return false, nil
		}

		if isExcluded(sender, excludedSenders) {
			slog.Info("skipping excluded sender", "sender", sender)
			return false, nil
		}

		if err := database.UpsertRoute(sender, targetMbox.DisplayPath, targetID); err != nil {
			return false, fmt.Errorf("upsert route: %w", err)
		}
		slog.Info("learned route", "sender", sender, "target", targetMbox.DisplayPath)
		_ = database.SetBoolState(sieveDirtyKey, true)
		return true, nil
	}

	// Was in Junk, now not: user rescued from Junk (screen-in).
	if resolved.Junk != nil && contains(prevMailboxIDs, resolved.Junk.ID) && !contains(mailboxIDs, resolved.Junk.ID) {
		targetID := findNewMailbox(mailboxIDs, resolved.Junk.ID)
		if targetID == "" {
			return false, nil
		}

		targetMbox, ok := resolved.Index.ByID[targetID]
		if !ok {
			return false, nil
		}
		if targetMbox.ID == screenerID {
			return resetSenderRoute(database, email, "reset sender route (moved to screener)")
		}
		if resolved.Trash != nil && targetMbox.ID == resolved.Trash.ID {
			return false, nil
		}

		sender := db.NormalizeSender(extractSender(email))
		if sender == "" {
			return false, nil
		}
		if isExcluded(sender, excludedSenders) {
			return false, nil
		}

		if err := database.UpsertRoute(sender, targetMbox.DisplayPath, targetID); err != nil {
			return false, fmt.Errorf("upsert route from junk rescue: %w", err)
		}
		slog.Info("learned route (screen-in from junk)", "sender", sender, "target", targetMbox.DisplayPath)
		_ = database.SetBoolState(sieveDirtyKey, true)
		return true, nil
	}

	return false, nil
}

func resetSenderRoute(database *db.DB, email jmap.Email, message string) (bool, error) {
	sender := db.NormalizeSender(extractSender(email))
	if sender == "" {
		slog.Debug("no sender for email", "id", email.ID)
		return false, nil
	}

	deleted, err := deleteRouteIfExists(database, sender)
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	slog.Info(message, "sender", sender)
	_ = database.SetBoolState(sieveDirtyKey, true)
	return true, nil
}

func deleteRouteIfExists(database *db.DB, sender string) (bool, error) {
	route, err := database.GetRoute(sender)
	if err != nil {
		return false, err
	}
	if route == nil {
		return false, nil
	}
	if err := database.DeleteRoute(sender); err != nil {
		return false, err
	}
	return true, nil
}

func rescueJunkPage(client *jmap.Client, database *db.DB, resolved *ResolvedMailboxes, rescuedByMe map[string]bool, batchSize int) (bool, error) {
	if resolved.Junk == nil {
		return false, nil
	}
	position := 0
	movedAny := false
	for {
		ids, total, responsePosition, err := client.QueryEmails(resolved.Junk.ID, position, batchSize)
		if err != nil {
			return movedAny, fmt.Errorf("email/query for junk rescue: %w", err)
		}
		if len(ids) == 0 {
			return movedAny, nil
		}
		result, err := client.GetEmailsWithState(ids, "mailboxIds", "from", "sender")
		if err != nil {
			return movedAny, fmt.Errorf("email/get for junk rescue: %w", err)
		}
		for _, emailID := range result.NotFound {
			slog.Info("junk query email not found; deleting cache", "email", emailID)
			if err := database.DeleteEmailCache(emailID); err != nil {
				return movedAny, err
			}
		}
		emailByID := make(map[string]jmap.Email, len(result.List))
		for _, email := range result.List {
			emailByID[email.ID] = email
		}

		pageMoved := false
		for _, emailID := range ids {
			email, ok := emailByID[emailID]
			if !ok {
				continue
			}
			sender := db.NormalizeSender(extractSender(email))
			if sender == "" {
				continue
			}
			route, err := database.GetRoute(sender)
			if err != nil {
				return movedAny, err
			}
			if route == nil {
				continue
			}
			target, ok := resolved.Index.ByID[route.TargetMailboxID]
			if !ok {
				slog.Warn("skipping junk rescue for missing route target", "sender", sender, "target_mailbox_id", route.TargetMailboxID)
				continue
			}
			setErr, err := client.MoveEmail(emailID, target.ID)
			if err != nil {
				return movedAny, fmt.Errorf("rescue move %s: %w", emailID, err)
			}
			if setErr != nil {
				slog.Warn("rescue move notUpdated", "email", emailID, "type", setErr.Type, "desc", setErr.Description)
				continue
			}
			rescuedByMe[emailID] = true
			movedAny = true
			pageMoved = true
			if err := database.UpsertEmailCache(emailID, []string{target.ID}); err != nil {
				return movedAny, err
			}
			slog.Debug("rescued from junk", "sender", sender, "target", route.TargetMailbox)
		}

		if pageMoved {
			position = 0
			continue
		}
		position = responsePosition + len(ids)
		if queryPageComplete(position, total, len(ids), batchSize) {
			return movedAny, nil
		}
	}
}

func queryPageComplete(nextPosition, total, pageLen, limit int) bool {
	if pageLen < limit {
		return true
	}
	return total > 0 && nextPosition >= total
}

func extractSender(email jmap.Email) string {
	if len(email.From) > 0 && email.From[0].Email != "" {
		return email.From[0].Email
	}
	if len(email.Sender) > 0 && email.Sender[0].Email != "" {
		return email.Sender[0].Email
	}
	return ""
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

func findNewMailbox(currIDs []string, sourceID string) string {
	candidates := make([]string, 0, len(currIDs))
	for _, id := range currIDs {
		if id != sourceID {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 1 {
		return candidates[0]
	}
	if len(candidates) > 1 {
		slog.Warn("multiple target mailboxes after move; skipping learn", "source_mailbox_id", sourceID, "candidate_mailbox_ids", candidates)
	}
	return ""
}

func isExcluded(sender string, excluded []string) bool {
	lower := db.NormalizeSender(sender)
	for _, pattern := range excluded {
		if strings.HasPrefix(lower, db.NormalizeSender(pattern)) {
			return true
		}
	}
	return false
}
