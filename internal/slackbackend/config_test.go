package slackbackend

import (
	"os"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("CORE_URL", "http://core:8080")
	t.Setenv("PUBLIC_BASE_URL", "http://slack-backend:8081")
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != ":8081" {
		t.Errorf("ListenAddr = %q, want :8081", cfg.ListenAddr)
	}
	if cfg.BackendName != "slack" {
		t.Errorf("BackendName = %q, want slack", cfg.BackendName)
	}
	if cfg.RegisterInterval != 30*time.Second {
		t.Errorf("RegisterInterval = %v, want 30s", cfg.RegisterInterval)
	}
	if cfg.SlackRequestTimeout != 5*time.Second {
		t.Errorf("SlackRequestTimeout = %v, want 5s", cfg.SlackRequestTimeout)
	}
	if cfg.SlackMaxRetries != 3 {
		t.Errorf("SlackMaxRetries = %d, want 3", cfg.SlackMaxRetries)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name     string
		missing  string
		setOther func(t *testing.T)
	}{
		{
			name:    "missing slack bot token",
			missing: "SLACK_BOT_TOKEN",
			setOther: func(t *testing.T) {
				t.Setenv("CORE_URL", "http://core:8080")
				t.Setenv("PUBLIC_BASE_URL", "http://slack-backend:8081")
			},
		},
		{
			name:    "missing core url",
			missing: "CORE_URL",
			setOther: func(t *testing.T) {
				t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
				t.Setenv("PUBLIC_BASE_URL", "http://slack-backend:8081")
			},
		},
		{
			name:    "missing public base url",
			missing: "PUBLIC_BASE_URL",
			setOther: func(t *testing.T) {
				t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
				t.Setenv("CORE_URL", "http://core:8080")
			},
		},
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
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr error
	}{
		{name: "valid", mutate: func(c *Config) {}, wantErr: nil},
		{name: "zero register interval", mutate: func(c *Config) { c.RegisterInterval = 0 }, wantErr: ErrInvalidRegisterInterval},
		{name: "negative register interval", mutate: func(c *Config) { c.RegisterInterval = -time.Second }, wantErr: ErrInvalidRegisterInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{RegisterInterval: 30 * time.Second}
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err != tt.wantErr {
				t.Fatalf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
