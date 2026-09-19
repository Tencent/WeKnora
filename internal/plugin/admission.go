package plugin

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrPluginDraining     = errors.New("plugin runtime is draining")
	ErrPluginOverloaded   = errors.New("plugin request queue is full")
	ErrPluginQueueTimeout = errors.New("plugin request queue wait timed out")
	ErrPluginDrainTimeout = errors.New("plugin runtime drain timed out")
)

type AdmissionError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *AdmissionError) Error() string {
	if e == nil || e.Cause == nil {
		return "plugin admission rejected"
	}
	return e.Cause.Error()
}
func (e *AdmissionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// AdmissionConfig bounds concurrent and queued calls for one shared runtime.
type AdmissionConfig struct {
	MaxConcurrent int
	MaxQueued     int
	QueueTimeout  time.Duration
	DrainTimeout  time.Duration
}

func defaultAdmissionConfig() AdmissionConfig {
	return AdmissionConfig{MaxConcurrent: 4, MaxQueued: 100, QueueTimeout: 30 * time.Second, DrainTimeout: 30 * time.Second}
}

type admissionController struct {
	mu       sync.Mutex
	config   AdmissionConfig
	active   map[uint64]context.CancelCauseFunc
	nextCall uint64
	waiting  int
	draining bool
	notify   chan struct{}
	admitted uint64
	rejected uint64
	timedOut uint64
}

func newAdmissionController(config AdmissionConfig) *admissionController {
	defaults := defaultAdmissionConfig()
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaults.MaxConcurrent
	}
	if config.MaxQueued < 0 {
		config.MaxQueued = defaults.MaxQueued
	}
	if config.QueueTimeout <= 0 {
		config.QueueTimeout = defaults.QueueTimeout
	}
	if config.DrainTimeout <= 0 {
		config.DrainTimeout = defaults.DrainTimeout
	}
	return &admissionController{config: config, notify: make(chan struct{}), active: make(map[uint64]context.CancelCauseFunc)}
}

func (a *admissionController) acquire(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, a.config.QueueTimeout)
	defer cancel()
	for {
		a.mu.Lock()
		if a.draining {
			a.rejected++
			a.mu.Unlock()
			return nil, nil, &AdmissionError{Code: "plugin_draining", Retryable: true, RetryAfter: time.Second, Cause: ErrPluginDraining}
		}
		if len(a.active) < a.config.MaxConcurrent {
			a.nextCall++
			callID := a.nextCall
			callCtx, cancelCall := context.WithCancelCause(ctx)
			a.active[callID] = cancelCall
			a.admitted++
			a.mu.Unlock()
			var once sync.Once
			return callCtx, func() { once.Do(func() { a.release(callID) }) }, nil
		}
		if a.waiting >= a.config.MaxQueued {
			a.rejected++
			a.mu.Unlock()
			return nil, nil, &AdmissionError{Code: "plugin_overloaded", Retryable: true, RetryAfter: time.Second, Cause: ErrPluginOverloaded}
		}
		a.waiting++
		notify := a.notify
		a.mu.Unlock()

		select {
		case <-waitCtx.Done():
			a.mu.Lock()
			a.waiting--
			a.rejected++
			if ctx.Err() == nil {
				a.timedOut++
			}
			a.mu.Unlock()
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, &AdmissionError{Code: "plugin_queue_timeout", Retryable: true, RetryAfter: time.Second, Cause: ErrPluginQueueTimeout}
		case <-notify:
			a.mu.Lock()
			a.waiting--
			a.mu.Unlock()
		}
	}
}

func (a *admissionController) release(callID uint64) {
	a.mu.Lock()
	if cancel, ok := a.active[callID]; ok {
		delete(a.active, callID)
		cancel(nil)
	}
	a.signalLocked()
	a.mu.Unlock()
}

func (a *admissionController) cancelActive(cause error) {
	a.mu.Lock()
	for _, cancel := range a.active {
		cancel(cause)
	}
	a.mu.Unlock()
}

func (a *admissionController) beginDrain() {
	a.mu.Lock()
	a.draining = true
	a.signalLocked()
	a.mu.Unlock()
}

func (a *admissionController) resume() {
	a.mu.Lock()
	a.draining = false
	a.signalLocked()
	a.mu.Unlock()
}

func (a *admissionController) waitDrained(ctx context.Context) error {
	for {
		a.mu.Lock()
		if len(a.active) == 0 {
			a.mu.Unlock()
			return nil
		}
		notify := a.notify
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notify:
		}
	}
}

func (a *admissionController) signalLocked() {
	close(a.notify)
	a.notify = make(chan struct{})
}

type AdmissionStatus struct {
	Active        int    `json:"active"`
	Waiting       int    `json:"waiting"`
	Draining      bool   `json:"draining"`
	Admitted      uint64 `json:"admitted"`
	Rejected      uint64 `json:"rejected"`
	QueueTimeouts uint64 `json:"queue_timeouts"`
}

func (a *admissionController) status() AdmissionStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AdmissionStatus{Active: len(a.active), Waiting: a.waiting, Draining: a.draining, Admitted: a.admitted, Rejected: a.rejected, QueueTimeouts: a.timedOut}
}
