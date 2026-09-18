package config

import (
	"errors"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

var (
	ErrIdleWindowTooLong      = errors.New("IdleWindowTooLong")
	ErrMaxWaitTooLong         = errors.New("MaxWaitTooLong")
	ErrInvalidLogFormat       = errors.New("InvalidLogFormat")
	ErrInvalidDuplicateAction = errors.New("InvalidDuplicateAction")
)

type Config struct {
	ListenAddr      string `envconfig:"listen_addr" default:":8080"`
	EventsPath      string `envconfig:"events_path" default:"/events"`
	IngestQueueSize int    `envconfig:"ingest_queue_size" default:"1024"`
	WorkerCount     int    `envconfig:"worker_count" default:"4"`

	GroupLabel      string        `envconfig:"group_label" required:"true"`
	IdleWindow      time.Duration `envconfig:"idle_window" default:"30s"`
	MaxWait         time.Duration `envconfig:"max_wait" default:"5m"`
	CombineTriggers bool          `envconfig:"combine_triggers" default:"true"`
	SessionTTL      time.Duration `envconfig:"session_ttl" default:"45m"`
	DuplicateAction string        `envconfig:"duplicate_action" default:"drop"`

	SlackBotToken       string        `envconfig:"slack_bot_token" required:"true"`
	SlackRequestTimeout time.Duration `envconfig:"slack_request_timeout" default:"5s"`
	SlackMaxRetries     int           `envconfig:"slack_max_retries" default:"3"`

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
	var errs []error
	if c.IdleWindow >= c.MaxWait {
		errs = append(errs, ErrIdleWindowTooLong)
	}
	if c.MaxWait >= c.SessionTTL {
		errs = append(errs, ErrMaxWaitTooLong)
	}
	switch strings.ToLower(c.LogFormat) {
	case "json", "text":
	default:
		errs = append(errs, ErrInvalidLogFormat)
	}
	switch strings.ToLower(c.DuplicateAction) {
	case "drop", "thread":
	default:
		errs = append(errs, ErrInvalidDuplicateAction)
	}
	return errors.Join(errs...)
}
