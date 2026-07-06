// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	JMAP    JMAPConfig    `toml:"jmap"`
	DB      DBConfig      `toml:"db"`
	Daemon  DaemonConfig  `toml:"daemon"`
	Monitor MonitorConfig `toml:"monitor"`
	Rescue  RescueConfig  `toml:"rescue"`
	Health  HealthConfig  `toml:"health"`
	Logging LoggingConfig `toml:"logging"`
}

type JMAPConfig struct {
	URL         string `toml:"url"`
	Token       string `toml:"token"`
	AccountID   string `toml:"account_id"`
	PushEnabled bool   `toml:"push_enabled"`
}

type DBConfig struct {
	Path string `toml:"path"`
}

type DaemonConfig struct {
	PollIntervalSeconds int `toml:"poll_interval_seconds"`
}

func (d DaemonConfig) PollInterval() time.Duration {
	return time.Duration(d.PollIntervalSeconds) * time.Second
}

type MonitorConfig struct {
	ScreenerMailbox    string   `toml:"screener_mailbox"`
	JunkMailbox        string   `toml:"junk_mailbox"`
	SpamScoreThreshold int      `toml:"spam_score_threshold"`
	ExcludedSenders    []string `toml:"excluded_senders"`
}

type RescueConfig struct {
	Enabled   bool `toml:"enabled"`
	BatchSize int  `toml:"batch_size"`
}

type HealthConfig struct {
	Port int `toml:"port"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
}

func Load(path string) (*Config, error) {
	return LoadWithOptions(path, true)
}

func LoadWithOptions(path string, explicit bool) (*Config, error) {
	cfg := &Config{
		JMAP: JMAPConfig{
			PushEnabled: true,
		},
		DB: DBConfig{
			Path: "screener.db",
		},
		Daemon: DaemonConfig{
			PollIntervalSeconds: 300,
		},
		Monitor: MonitorConfig{
			ScreenerMailbox:    "Screener",
			JunkMailbox:        "Junk Mail",
			SpamScoreThreshold: 5,
			ExcludedSenders:    []string{"mailer-daemon@", "postmaster@"},
		},
		Rescue: RescueConfig{
			Enabled:   false,
			BatchSize: 100,
		},
		Health: HealthConfig{
			Port: 8080,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}

	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) && !explicit {
				// Omitted --config may run env-only when config.toml is absent.
			} else {
				return nil, fmt.Errorf("failed to access config file %s: %w", path, err)
			}
		} else if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
		}
	}

	if v := os.Getenv("SCREENER_JMAP_URL"); v != "" {
		cfg.JMAP.URL = v
	}
	if v := os.Getenv("SCREENER_JMAP_TOKEN"); v != "" {
		cfg.JMAP.Token = v
	}
	if v := os.Getenv("SCREENER_JMAP_ACCOUNT_ID"); v != "" {
		cfg.JMAP.AccountID = v
	}
	if v := os.Getenv("SCREENER_JMAP_PUSH_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("SCREENER_JMAP_PUSH_ENABLED must be a boolean")
		}
		cfg.JMAP.PushEnabled = enabled
	}
	if v := os.Getenv("SCREENER_DB_PATH"); v != "" {
		cfg.DB.Path = v
	}
	if v := os.Getenv("SCREENER_MONITOR_SCREENER_MAILBOX"); v != "" {
		cfg.Monitor.ScreenerMailbox = v
	}
	if v := os.Getenv("SCREENER_MONITOR_JUNK_MAILBOX"); v != "" {
		cfg.Monitor.JunkMailbox = v
	}
	if v := os.Getenv("SCREENER_MONITOR_SPAM_SCORE_THRESHOLD"); v != "" {
		threshold, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("SCREENER_MONITOR_SPAM_SCORE_THRESHOLD must be an integer")
		}
		cfg.Monitor.SpamScoreThreshold = threshold
	}
	if v := os.Getenv("SCREENER_LOGGING_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}

	if cfg.JMAP.URL == "" {
		return nil, fmt.Errorf("jmap.url is required")
	}
	if cfg.JMAP.Token == "" {
		return nil, fmt.Errorf("jmap.token is required (set SCREENER_JMAP_TOKEN or config file)")
	}
	if cfg.Daemon.PollIntervalSeconds <= 0 {
		return nil, fmt.Errorf("daemon.poll_interval_seconds must be positive")
	}
	if cfg.Rescue.BatchSize <= 0 {
		return nil, fmt.Errorf("rescue.batch_size must be positive")
	}
	if cfg.Monitor.SpamScoreThreshold <= 0 {
		return nil, fmt.Errorf("monitor.spam_score_threshold must be positive")
	}
	if cfg.Health.Port <= 0 {
		return nil, fmt.Errorf("health.port must be positive")
	}
	cfg.Logging.Level = strings.ToLower(cfg.Logging.Level)
	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("logging.level must be one of debug, info, warn, error")
	}

	return cfg, nil
}
