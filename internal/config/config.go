package config

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

var (
	ErrIdleWindowTooLong              = errors.New("IdleWindowTooLong")
	ErrMaxWaitTooLong                 = errors.New("MaxWaitTooLong")
	ErrInvalidLogFormat               = errors.New("InvalidLogFormat")
	ErrInvalidDuplicateAction         = errors.New("InvalidDuplicateAction")
	ErrMissingLeaderElectionNamespace = errors.New("MissingLeaderElectionNamespace")
	ErrLeaseDurationTooShort          = errors.New("LeaseDurationTooShort")
	ErrRenewDeadlineTooShort          = errors.New("RenewDeadlineTooShort")
	ErrPprofAddrConflict              = errors.New("PprofAddrConflict")
	ErrBackendNotSupported            = errors.New("BackendNotSupported")
	ErrMissingSlackBotToken           = errors.New("MissingSlackBotToken")
)

type Config struct {
	Backend         string `envconfig:"backend" default:"slack"`
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

	SlackBotToken       string        `envconfig:"slack_bot_token"`
	SlackRequestTimeout time.Duration `envconfig:"slack_request_timeout" default:"5s"`
	SlackMaxRetries     int           `envconfig:"slack_max_retries" default:"3"`

	LogLevel  string `envconfig:"log_level" default:"info"`
	LogFormat string `envconfig:"log_format" default:"json"`

	LeaderElectionEnabled   bool          `envconfig:"leader_election_enabled" default:"false"`
	LeaderElectionNamespace string        `envconfig:"leader_election_namespace"`
	LeaseName               string        `envconfig:"lease_name" default:"argocd-notifier-leader"`
	LeaseDuration           time.Duration `envconfig:"lease_duration" default:"15s"`
	RenewDeadline           time.Duration `envconfig:"renew_deadline" default:"10s"`
	RetryPeriod             time.Duration `envconfig:"retry_period" default:"2s"`
	// PodName is the leader-election identity; falls back to os.Hostname()
	// (the pod name, in-cluster) in Load() when unset.
	PodName string `envconfig:"pod_name"`

	// PprofEnabled serves net/http/pprof on its own listener, separate from
	// the main server — never put behind the k8s Service ArgoCD's webhook
	// and readiness checks route through.
	PprofEnabled bool   `envconfig:"pprof_enabled" default:"false"`
	PprofAddr    string `envconfig:"pprof_addr" default:":6060"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}

	if cfg.PodName == "" {
		if hostname, err := os.Hostname(); err == nil {
			cfg.PodName = hostname
		}
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
	if c.Backend == "slack" && c.SlackBotToken == "" {
		errs = append(errs, ErrMissingSlackBotToken)
	}
	if c.LeaderElectionEnabled {
		if c.LeaderElectionNamespace == "" {
			errs = append(errs, ErrMissingLeaderElectionNamespace)
		}
		if c.LeaseDuration <= c.RenewDeadline {
			errs = append(errs, ErrLeaseDurationTooShort)
		}
		if c.RenewDeadline <= c.RetryPeriod {
			errs = append(errs, ErrRenewDeadlineTooShort)
		}
	}
	if c.PprofEnabled && c.PprofAddr == c.ListenAddr {
		errs = append(errs, ErrPprofAddrConflict)
	}
	return errors.Join(errs...)
}
