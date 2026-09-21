package leader

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type Config struct {
	Namespace     string
	LeaseName     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

type Elector struct {
	le            *leaderelection.LeaderElector
	leading       atomic.Bool
	currentLeader atomic.Pointer[string]
}

func New(client kubernetes.Interface, cfg Config, logger *slog.Logger) (*Elector, error) {
	e := &Elector{}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: cfg.LeaseName, Namespace: cfg.Namespace},
		Client:    client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				logger.Info("acquired leadership", "identity", cfg.Identity)
				e.leading.Store(true)
			},
			OnStoppedLeading: func() {
				logger.Info("lost leadership", "identity", cfg.Identity)
				e.leading.Store(false)
			},
			OnNewLeader: func(identity string) {
				if identity != cfg.Identity {
					logger.Info("new leader elected", "identity", identity)
				}
				e.currentLeader.Store(&identity)
			},
		},
	})
	if err != nil {
		return nil, err
	}

	e.le = le
	return e, nil
}

func (e *Elector) IsLeader() bool {
	return e.leading.Load()
}

// CurrentLeader reports the identity of whoever this instance last observed
// holding the lease — used to address a request at the leader when this
// instance isn't it. ok is false only before any leader has ever been
// observed (e.g. immediately after startup, before the first election).
func (e *Elector) CurrentLeader() (identity string, ok bool) {
	v := e.currentLeader.Load()
	if v == nil {
		return "", false
	}
	return *v, true
}

// Run blocks until ctx is canceled, repeatedly campaigning for leadership.
func (e *Elector) Run(ctx context.Context) {
	e.le.Run(ctx)
}
