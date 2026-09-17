package config

import (
	"errors"
	"os"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Setenv("GROUP_LABEL", "application/name")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.EventsPath != "/events" {
		t.Errorf("EventsPath = %q, want /events", cfg.EventsPath)
	}
	if cfg.IdleWindow != 30*time.Second {
		t.Errorf("IdleWindow = %v, want 30s", cfg.IdleWindow)
	}
	if cfg.MaxWait != 5*time.Minute {
		t.Errorf("MaxWait = %v, want 5m", cfg.MaxWait)
	}
	if cfg.SessionTTL != 45*time.Minute {
		t.Errorf("SessionTTL = %v, want 45m", cfg.SessionTTL)
	}
	if cfg.DedupTTL != 25*time.Minute {
		t.Errorf("DedupTTL = %v, want 25m (5x default MaxWait)", cfg.DedupTTL)
	}
	if !cfg.CombineTriggers {
		t.Error("CombineTriggers = false, want true")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
}

func TestLoad_DedupTTLOverride(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DEDUP_TTL", "10m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DedupTTL != 10*time.Minute {
		t.Errorf("DedupTTL = %v, want 10m (explicit override)", cfg.DedupTTL)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name     string
		missing  string
		setOther func(t *testing.T)
	}{
		{name: "missing group label", missing: "GROUP_LABEL", setOther: func(t *testing.T) { t.Setenv("SLACK_BOT_TOKEN", "xoxb-test") }},
		{name: "missing slack bot token", missing: "SLACK_BOT_TOKEN", setOther: func(t *testing.T) { t.Setenv("GROUP_LABEL", "application/name") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv(tt.missing)
			tt.setOther(t)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() = nil error, want error for missing %s", tt.missing)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	base := func() Config {
		return Config{
			GroupLabel:    "application/name",
			IdleWindow:    30 * time.Second,
			MaxWait:       5 * time.Minute,
			SessionTTL:    45 * time.Minute,
			SlackBotToken: "xoxb-test",
			LogFormat:     "json",
		}
	}

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr error
	}{
		{name: "valid", mutate: func(c *Config) {}, wantErr: nil},
		{name: "idle window too long", mutate: func(c *Config) { c.IdleWindow = c.MaxWait }, wantErr: ErrIdleWindowTooLong},
		{name: "max wait too long", mutate: func(c *Config) { c.MaxWait = c.SessionTTL }, wantErr: ErrMaxWaitTooLong},
		{name: "invalid log format", mutate: func(c *Config) { c.LogFormat = "xml" }, wantErr: ErrInvalidLogFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}
