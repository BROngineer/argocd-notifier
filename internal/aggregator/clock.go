package aggregator

import "time"

type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
}

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) Stop() bool {
	return r.t.Stop()
}

func (r *realTimer) Reset(d time.Duration) bool {
	return r.t.Reset(d)
}
