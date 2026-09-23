package slackbackend

import (
	"errors"
	"time"

	"github.com/kelseyhightower/envconfig"
)

var (
	ErrInvalidRegisterInterval              = errors.New("InvalidRegisterInterval")
	ErrInvalidMessageTemplateReloadInterval = errors.New("InvalidMessageTemplateReloadInterval")
)

type Config struct {
	SlackBotToken       string        `envconfig:"slack_bot_token" required:"true"`
	SlackRequestTimeout time.Duration `envconfig:"slack_request_timeout" default:"5s"`
	SlackMaxRetries     int           `envconfig:"slack_max_retries" default:"3"`

	ListenAddr string `envconfig:"listen_addr" default:":8081"`

	// CoreURL is the core's base address — this backend registers against
	// {CoreURL}/v1/backends/register.
	CoreURL string `envconfig:"core_url" required:"true"`
	// PublicBaseURL is this backend's own reachable address, told to the
	// core at registration; the core appends /notify and /thread-reply.
	PublicBaseURL string `envconfig:"public_base_url" required:"true"`
	// BackendName is what events must name in their `backend` field
	// (via the application/backend Application label) to route here.
	BackendName string `envconfig:"backend_name" default:"slack"`
	// RegisterInterval must stay comfortably under the core's own
	// BACKEND_REGISTRY_TTL (default 90s), or this backend goes stale
	// between heartbeats.
	RegisterInterval time.Duration `envconfig:"register_interval" default:"30s"`

	// MessageTemplatePath enables a custom Slack message template (see
	// internal/slack.TemplateWatcher) when set; empty (the default) keeps
	// the built-in DefaultRenderer, unchanged.
	MessageTemplatePath string `envconfig:"message_template_path"`
	// MessageTemplateReloadInterval is only used when MessageTemplatePath
	// is set.
	MessageTemplateReloadInterval time.Duration `envconfig:"message_template_reload_interval" default:"30s"`
	// MessageTemplateFallbackToDefault: when a loaded template fails to
	// render a specific notification, true falls back to DefaultRenderer
	// so the message still gets sent; false (the default) logs and drops
	// just that notification instead of silently changing its look.
	MessageTemplateFallbackToDefault bool `envconfig:"message_template_fallback_to_default" default:"false"`

	LogLevel  string `envconfig:"log_level" default:"info"`
	LogFormat string `envconfig:"log_format" default:"json"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.RegisterInterval <= 0 {
		return ErrInvalidRegisterInterval
	}
	if c.MessageTemplateReloadInterval <= 0 {
		return ErrInvalidMessageTemplateReloadInterval
	}
	return nil
}
