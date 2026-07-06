// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_FullConfig(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
account_id = "test@example.com"

[db]
path = "/tmp/test.db"

[daemon]
poll_interval_seconds = 60

[monitor]
screener_mailbox = "Inbox"
junk_mailbox = "Spam"
spam_score_threshold = 8
excluded_senders = ["blocked@", "noreply@"]

[rescue]
enabled = false
batch_size = 50

[health]
port = 9090

[logging]
level = "debug"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.JMAP.URL != "https://example.com/api" {
		t.Errorf("JMAP.URL = %q, want %q", cfg.JMAP.URL, "https://example.com/api")
	}
	if cfg.JMAP.Token != "test-token" {
		t.Errorf("JMAP.Token = %q, want %q", cfg.JMAP.Token, "test-token")
	}
	if cfg.JMAP.AccountID != "test@example.com" {
		t.Errorf("JMAP.AccountID = %q, want %q", cfg.JMAP.AccountID, "test@example.com")
	}
	if !cfg.JMAP.PushEnabled {
		t.Error("JMAP.PushEnabled = false, want true")
	}
	if cfg.DB.Path != "/tmp/test.db" {
		t.Errorf("DB.Path = %q, want %q", cfg.DB.Path, "/tmp/test.db")
	}
	if cfg.Daemon.PollIntervalSeconds != 60 {
		t.Errorf("Daemon.PollIntervalSeconds = %d, want 60", cfg.Daemon.PollIntervalSeconds)
	}
	if cfg.Monitor.ScreenerMailbox != "Inbox" {
		t.Errorf("Monitor.ScreenerMailbox = %q, want %q", cfg.Monitor.ScreenerMailbox, "Inbox")
	}
	if cfg.Monitor.JunkMailbox != "Spam" {
		t.Errorf("Monitor.JunkMailbox = %q, want %q", cfg.Monitor.JunkMailbox, "Spam")
	}
	if cfg.Monitor.SpamScoreThreshold != 8 {
		t.Errorf("Monitor.SpamScoreThreshold = %d, want 8", cfg.Monitor.SpamScoreThreshold)
	}
	if len(cfg.Monitor.ExcludedSenders) != 2 ||
		cfg.Monitor.ExcludedSenders[0] != "blocked@" ||
		cfg.Monitor.ExcludedSenders[1] != "noreply@" {
		t.Errorf("Monitor.ExcludedSenders = %v, want [blocked@ noreply@]", cfg.Monitor.ExcludedSenders)
	}
	if cfg.Rescue.Enabled != false {
		t.Errorf("Rescue.Enabled = %v, want false", cfg.Rescue.Enabled)
	}
	if cfg.Rescue.BatchSize != 50 {
		t.Errorf("Rescue.BatchSize = %d, want 50", cfg.Rescue.BatchSize)
	}
	if cfg.Health.Port != 9090 {
		t.Errorf("Health.Port = %d, want 9090", cfg.Health.Port)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
}

func TestLoad_MinimalConfig(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.JMAP.URL != "https://example.com/api" {
		t.Errorf("JMAP.URL = %q", cfg.JMAP.URL)
	}
	if cfg.JMAP.Token != "test-token" {
		t.Errorf("JMAP.Token = %q", cfg.JMAP.Token)
	}
	if cfg.JMAP.AccountID != "" {
		t.Errorf("JMAP.AccountID = %q, want empty for auto-discovery", cfg.JMAP.AccountID)
	}
	if !cfg.JMAP.PushEnabled {
		t.Error("JMAP.PushEnabled = false, want true")
	}
	if cfg.DB.Path != "screener.db" {
		t.Errorf("DB.Path = %q, want %q", cfg.DB.Path, "screener.db")
	}
	if cfg.Daemon.PollIntervalSeconds != 300 {
		t.Errorf("Daemon.PollIntervalSeconds = %d, want 300", cfg.Daemon.PollIntervalSeconds)
	}
	if cfg.Monitor.ScreenerMailbox != "Screener" {
		t.Errorf("Monitor.ScreenerMailbox = %q, want %q", cfg.Monitor.ScreenerMailbox, "Screener")
	}
	if cfg.Monitor.JunkMailbox != "Junk Mail" {
		t.Errorf("Monitor.JunkMailbox = %q, want %q", cfg.Monitor.JunkMailbox, "Junk Mail")
	}
	if cfg.Monitor.SpamScoreThreshold != 5 {
		t.Errorf("Monitor.SpamScoreThreshold = %d, want 5", cfg.Monitor.SpamScoreThreshold)
	}
	if len(cfg.Monitor.ExcludedSenders) != 2 {
		t.Errorf("Monitor.ExcludedSenders len = %d, want 2", len(cfg.Monitor.ExcludedSenders))
	}
	if cfg.Rescue.Enabled != false {
		t.Errorf("Rescue.Enabled = %v, want false", cfg.Rescue.Enabled)
	}
	if cfg.Rescue.BatchSize != 100 {
		t.Errorf("Rescue.BatchSize = %d, want 100", cfg.Rescue.BatchSize)
	}
	if cfg.Health.Port != 8080 {
		t.Errorf("Health.Port = %d, want 8080", cfg.Health.Port)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "info")
	}
}

func TestLoad_MissingURL(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing jmap.url")
	}
	if !strings.Contains(err.Error(), "jmap.url is required") {
		t.Errorf("error = %q, want containing 'jmap.url is required'", err.Error())
	}
}

func TestLoad_MissingToken(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
`
	path := writeTempTOML(t, tomlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing jmap.token")
	}
	if !strings.Contains(err.Error(), "jmap.token is required") {
		t.Errorf("error = %q, want containing 'jmap.token is required'", err.Error())
	}
}

func TestLoad_EnvOverrideURL(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_URL", "https://override.example.com/api")

	tomlContent := `
[jmap]
url = "https://file.example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.JMAP.URL != "https://override.example.com/api" {
		t.Errorf("JMAP.URL = %q, want override value", cfg.JMAP.URL)
	}
}

func TestLoad_EnvOverrideToken(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_TOKEN", "env-token")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "file-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.JMAP.Token != "env-token" {
		t.Errorf("JMAP.Token = %q, want %q", cfg.JMAP.Token, "env-token")
	}
}

func TestLoad_EnvOverrideAccountID(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_ACCOUNT_ID", "user@example.com")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.JMAP.AccountID != "user@example.com" {
		t.Errorf("JMAP.AccountID = %q, want %q", cfg.JMAP.AccountID, "user@example.com")
	}
}

func TestLoad_JMAPPushEnabled(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
push_enabled = false
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.JMAP.PushEnabled {
		t.Error("JMAP.PushEnabled = true, want false")
	}
}

func TestLoad_EnvOverrideJMAPPushEnabled(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_PUSH_ENABLED", "false")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
push_enabled = true
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.JMAP.PushEnabled {
		t.Error("JMAP.PushEnabled = true, want env override false")
	}
}

func TestLoad_EnvOverrideDBPath(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_DB_PATH", "/tmp/test.db")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.DB.Path != "/tmp/test.db" {
		t.Errorf("DB.Path = %q, want %q", cfg.DB.Path, "/tmp/test.db")
	}
}

func TestLoad_EnvOverrideScreenerMailbox(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_MONITOR_SCREENER_MAILBOX", "Review")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[monitor]
screener_mailbox = "Screener"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Monitor.ScreenerMailbox != "Review" {
		t.Errorf("Monitor.ScreenerMailbox = %q, want %q", cfg.Monitor.ScreenerMailbox, "Review")
	}
}

func TestLoad_EnvOverrideJunkMailbox(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_MONITOR_JUNK_MAILBOX", "Spam")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[monitor]
junk_mailbox = "Junk"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Monitor.JunkMailbox != "Spam" {
		t.Errorf("Monitor.JunkMailbox = %q, want %q", cfg.Monitor.JunkMailbox, "Spam")
	}
}

func TestLoad_EnvOverrideSpamScoreThreshold(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_MONITOR_SPAM_SCORE_THRESHOLD", "9")

	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[monitor]
spam_score_threshold = 5
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Monitor.SpamScoreThreshold != 9 {
		t.Errorf("Monitor.SpamScoreThreshold = %d, want 9", cfg.Monitor.SpamScoreThreshold)
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	cleanScreenerEnv(t)
	_, err := Load("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoadWithOptions_OmittedMissingConfigUsesEnv(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_URL", "https://env.example.com/api")
	t.Setenv("SCREENER_JMAP_TOKEN", "env-token")

	cfg, err := LoadWithOptions(filepath.Join(t.TempDir(), "config.toml"), false)
	if err != nil {
		t.Fatalf("LoadWithOptions() error: %v", err)
	}
	if cfg.JMAP.URL != "https://env.example.com/api" {
		t.Errorf("JMAP.URL = %q", cfg.JMAP.URL)
	}
	if cfg.JMAP.Token != "env-token" {
		t.Errorf("JMAP.Token = %q", cfg.JMAP.Token)
	}
}

func TestLoadWithOptions_ExplicitMissingConfigFatal(t *testing.T) {
	cleanScreenerEnv(t)
	t.Setenv("SCREENER_JMAP_URL", "https://env.example.com/api")
	t.Setenv("SCREENER_JMAP_TOKEN", "env-token")

	_, err := LoadWithOptions(filepath.Join(t.TempDir(), "config.toml"), true)
	if err == nil {
		t.Fatal("expected explicit missing config error")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `this is not valid toml {{{`
	path := writeTempTOML(t, tomlContent)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestLoad_PollInterval(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[daemon]
poll_interval_seconds = 60
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if interval := cfg.Daemon.PollInterval(); interval != 60*time.Second {
		t.Errorf("PollInterval() = %v, want 60s", interval)
	}
}

func TestLoad_DefaultPollInterval(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if interval := cfg.Daemon.PollInterval(); interval != 5*time.Minute {
		t.Errorf("PollInterval() = %v, want 5m", interval)
	}
}

func TestLoad_ExcludedSenders(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[monitor]
excluded_senders = ["spam@", "no-reply@", "noreply@"]
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Monitor.ExcludedSenders) != 3 {
		t.Fatalf("len(ExcludedSenders) = %d, want 3", len(cfg.Monitor.ExcludedSenders))
	}
	if cfg.Monitor.ExcludedSenders[0] != "spam@" {
		t.Errorf("[0] = %q, want %q", cfg.Monitor.ExcludedSenders[0], "spam@")
	}
	if cfg.Monitor.ExcludedSenders[1] != "no-reply@" {
		t.Errorf("[1] = %q, want %q", cfg.Monitor.ExcludedSenders[1], "no-reply@")
	}
	if cfg.Monitor.ExcludedSenders[2] != "noreply@" {
		t.Errorf("[2] = %q, want %q", cfg.Monitor.ExcludedSenders[2], "noreply@")
	}
}

func TestLoad_DefaultLogLevel(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "info")
	}
}

func TestLoad_CustomLogLevel(t *testing.T) {
	cleanScreenerEnv(t)
	tomlContent := `
[jmap]
url = "https://example.com/api"
token = "test-token"

[logging]
level = "debug"
`
	path := writeTempTOML(t, tomlContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
}

func TestLoad_InvalidValidationValues(t *testing.T) {
	cleanScreenerEnv(t)
	tests := []struct {
		name string
		body string
	}{
		{"poll", "[daemon]\npoll_interval_seconds = 0\n"},
		{"rescue", "[rescue]\nbatch_size = 0\n"},
		{"spam threshold", "[monitor]\nspam_score_threshold = 0\n"},
		{"health", "[health]\nport = 0\n"},
		{"logging", "[logging]\nlevel = \"trace\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTOML(t, `
[jmap]
url = "https://example.com/api"
token = "test-token"
`+tt.body)
			if _, err := Load(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func cleanScreenerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SCREENER_JMAP_URL", "")
	t.Setenv("SCREENER_JMAP_TOKEN", "")
	t.Setenv("SCREENER_JMAP_ACCOUNT_ID", "")
	t.Setenv("SCREENER_JMAP_PUSH_ENABLED", "")
	t.Setenv("SCREENER_DB_PATH", "")
	t.Setenv("SCREENER_MONITOR_SCREENER_MAILBOX", "")
	t.Setenv("SCREENER_MONITOR_JUNK_MAILBOX", "")
	t.Setenv("SCREENER_MONITOR_SPAM_SCORE_THRESHOLD", "")
	t.Setenv("SCREENER_LOGGING_LEVEL", "")
}
