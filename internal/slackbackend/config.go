package slackbackend

import (
	"errors"
	"time"

	"github.com/kelseyhightower/envconfig"
)

var ErrInvalidRegisterInterval = errors.New("InvalidRegisterInterval")

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
	return nil
}
