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

	if cfg.Backend != "slack" {
		t.Errorf("Backend = %q, want slack", cfg.Backend)
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
	if cfg.DuplicateAction != "drop" {
		t.Errorf("DuplicateAction = %q, want drop", cfg.DuplicateAction)
	}
	if !cfg.CombineTriggers {
		t.Error("CombineTriggers = false, want true")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
	if cfg.LeaderElectionEnabled {
		t.Error("LeaderElectionEnabled = true, want false")
	}
	if cfg.LeaseName != "argocd-notifier-leader" {
		t.Errorf("LeaseName = %q, want argocd-notifier-leader", cfg.LeaseName)
	}
	if cfg.PodName == "" {
		t.Error("PodName = \"\", want hostname fallback")
	}
	if cfg.PprofEnabled {
		t.Error("PprofEnabled = true, want false")
	}
	if cfg.PprofAddr != ":6060" {
		t.Errorf("PprofAddr = %q, want :6060", cfg.PprofAddr)
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
			GroupLabel:      "application/name",
			IdleWindow:      30 * time.Second,
			MaxWait:         5 * time.Minute,
			SessionTTL:      45 * time.Minute,
			SlackBotToken:   "xoxb-test",
			LogFormat:       "json",
			DuplicateAction: "drop",
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
		{name: "invalid duplicate action", mutate: func(c *Config) { c.DuplicateAction = "explode" }, wantErr: ErrInvalidDuplicateAction},
		{
			name: "leader election missing namespace",
			mutate: func(c *Config) {
				c.LeaderElectionEnabled = true
				c.LeaseDuration, c.RenewDeadline, c.RetryPeriod = 15*time.Second, 10*time.Second, 2*time.Second
			},
			wantErr: ErrMissingLeaderElectionNamespace,
		},
		{
			name: "leader election lease duration too short",
			mutate: func(c *Config) {
				c.LeaderElectionEnabled = true
				c.LeaderElectionNamespace = "argocd"
				c.LeaseDuration, c.RenewDeadline, c.RetryPeriod = 5*time.Second, 10*time.Second, 2*time.Second
			},
			wantErr: ErrLeaseDurationTooShort,
		},
		{
			name: "leader election renew deadline too short",
			mutate: func(c *Config) {
				c.LeaderElectionEnabled = true
				c.LeaderElectionNamespace = "argocd"
				c.LeaseDuration, c.RenewDeadline, c.RetryPeriod = 15*time.Second, 2*time.Second, 5*time.Second
			},
			wantErr: ErrRenewDeadlineTooShort,
		},
		{
			name: "leader election valid",
			mutate: func(c *Config) {
				c.LeaderElectionEnabled = true
				c.LeaderElectionNamespace = "argocd"
				c.LeaseDuration, c.RenewDeadline, c.RetryPeriod = 15*time.Second, 10*time.Second, 2*time.Second
			},
			wantErr: nil,
		},
		{
			name: "pprof addr conflicts with listen addr",
			mutate: func(c *Config) {
				c.PprofEnabled = true
				c.PprofAddr = c.ListenAddr
			},
			wantErr: ErrPprofAddrConflict,
		},
		{
			name: "pprof enabled with distinct addr",
			mutate: func(c *Config) {
				c.ListenAddr = ":8080"
				c.PprofEnabled = true
				c.PprofAddr = ":6060"
			},
			wantErr: nil,
		},
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
