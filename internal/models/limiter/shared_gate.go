package limiter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	ErrSharedQueueFull         = errors.New("shared model admission queue full")
	ErrSharedModelUnregistered = errors.New("model endpoint not registered for shared admission")
	ErrSharedGateBusy          = errors.New("cannot replace shared admission while calls are active or waiting")
)

type SharedGateConfig struct {
	Enabled          bool
	Concurrency      int // Process-wide active bound, including distinct endpoint groups.
	MaxWaiting       int // Process-wide waiting bound, across all endpoint groups.
	InteractiveBurst int // At most this many interactive grants while background work waits.
	MaxWait          time.Duration
	// ResourceGroups bind different endpoints to one physical resource, with one active call.
	ResourceGroups map[string][]string
}
type sharedWaiter struct {
	ready      chan struct{}
	background bool
	granted    bool
	group      *sharedGroup
}
type sharedGroup struct{ active, waiting, limit int }
type sharedAdmission struct {
	mu                     sync.Mutex
	config                 SharedGateConfig
	resources              map[string]string
	models                 map[string]string
	groups                 map[string]*sharedGroup
	waiting, active, burst int
	foreground, background []*sharedWaiter
}

var sharedGate = &sharedAdmission{models: map[string]string{}, groups: map[string]*sharedGroup{}}

// ConfigureSharedGate is a startup/configuration operation. Disabling or replacing
// a busy gate is rejected so existing leases cannot silently lose their budget.
func ConfigureSharedGate(c SharedGateConfig) error {
	if c.Enabled && (c.Concurrency < 1 || c.MaxWaiting < 0 || c.InteractiveBurst < 1 || c.MaxWait <= 0) {
		return errors.New("invalid shared admission configuration")
	}
	resources := map[string]string{}
	for name, endpoints := range c.ResourceGroups {
		if strings.TrimSpace(name) == "" || len(endpoints) == 0 {
			return errors.New("invalid shared resource group")
		}
		for _, endpoint := range endpoints {
			key, err := EndpointGroup(endpoint)
			if err != nil {
				return err
			}
			if _, exists := resources[key]; exists {
				return errors.New("endpoint belongs to multiple resource groups")
			}
			resources[key] = "resource-" + fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
		}
	}
	sharedGate.mu.Lock()
	defer sharedGate.mu.Unlock()
	if sharedGate.waiting != 0 {
		return ErrSharedGateBusy
	}
	for _, g := range sharedGate.groups {
		if g.active != 0 {
			return ErrSharedGateBusy
		}
	}
	sharedGate.config = c
	sharedGate.resources = resources
	sharedGate.groups = map[string]*sharedGroup{}
	sharedGate.foreground = nil
	sharedGate.background = nil
	sharedGate.burst = 0
	return nil
}

// EndpointGroup normalizes the transport authority, not the model UUID/name or
// URL path. Providers on different ports remain distinct; aliases/path variants
// on one authority share a budget. Credentials/query are rejected, never logged.
func EndpointGroup(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid model endpoint for admission")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	authority := u.Scheme + "://" + net.JoinHostPort(host, port)
	return fmt.Sprintf("endpoint-%x", sha256.Sum256([]byte(authority))), nil
}
func RegisterModelEndpoint(modelID, endpoint string) error {
	if strings.TrimSpace(modelID) == "" {
		return errors.New("missing model ID for admission")
	}
	key, err := EndpointGroup(endpoint)
	if err != nil {
		return err
	}
	sharedGate.mu.Lock()
	defer sharedGate.mu.Unlock()
	if resource, ok := sharedGate.resources[key]; ok {
		key = resource
	}
	if old, ok := sharedGate.models[modelID]; ok && old != key {
		if g := sharedGate.groups[old]; g != nil && (g.active != 0 || g.waiting != 0) {
			return ErrSharedGateBusy
		}
	}
	if _, ok := sharedGate.models[modelID]; !ok && len(sharedGate.models) >= 4096 {
		return errors.New("shared admission model registration bound exceeded")
	}
	sharedGate.models[modelID] = key
	return nil
}

// AcquireCall must be called by wrappers BEFORE invoking the provider, and its
// error must be returned without executing the call. Default-off keeps legacy
// background/fail-open semantics; strict mode applies to every call class.
func AcquireCall(ctx context.Context, modelID, modelName string, modelLimit int) (func(), error) {
	return sharedGate.acquire(ctx, modelID, modelName, modelLimit)
}
func (s *sharedAdmission) acquire(ctx context.Context, modelID, modelName string, modelLimit int) (func(), error) {
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return GateNamedN(ctx, modelID, modelName, modelLimit), nil
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	key, ok := s.models[modelID]
	if !ok {
		s.mu.Unlock()
		return nil, ErrSharedModelUnregistered
	}
	g := s.groups[key]
	if g == nil {
		if len(s.groups) >= 256 {
			s.mu.Unlock()
			return nil, errors.New("shared admission endpoint group bound exceeded")
		}
		limit := modelLimit
		if strings.HasPrefix(key, "resource-") {
			limit = 1
		}
		if limit <= 0 {
			limit = 1
		}
		g = &sharedGroup{limit: limit}
		s.groups[key] = g
	}
	// Alias models share the strictest positive provider limit.
	if modelLimit > 0 && (g.limit == 0 || modelLimit < g.limit) {
		g.limit = modelLimit
	}
	if s.active < s.config.Concurrency && g.active < g.limit && s.waiting == 0 {
		s.active++
		g.active++
		s.mu.Unlock()
		return s.lease(g), nil
	}
	if s.waiting >= s.config.MaxWaiting {
		s.mu.Unlock()
		return nil, ErrSharedQueueFull
	}
	w := &sharedWaiter{ready: make(chan struct{}), background: types.IsBackgroundTask(ctx), group: g}
	if w.background {
		s.background = append(s.background, w)
	} else {
		s.foreground = append(s.foreground, w)
	}
	s.waiting++
	g.waiting++
	s.dispatch()
	wait := s.config.MaxWait
	s.mu.Unlock()
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case <-w.ready:
		// Context cancellation must win even if grant and cancellation race.
		if err := waitCtx.Err(); err != nil {
			s.lease(g)()
			return nil, err
		}
		return s.lease(g), nil
	case <-waitCtx.Done():
		s.mu.Lock()
		if w.granted {
			g.active--
			s.active--
		} else {
			if w.background {
				s.background = removeSharedWaiter(s.background, w)
			} else {
				s.foreground = removeSharedWaiter(s.foreground, w)
			}
			s.waiting--
			g.waiting--
		}
		s.dispatch()
		s.mu.Unlock()
		return nil, waitCtx.Err()
	}
}
func removeSharedWaiter(q []*sharedWaiter, w *sharedWaiter) []*sharedWaiter {
	for i, v := range q {
		if v == w {
			return append(q[:i], q[i+1:]...)
		}
	}
	return q
}
func (s *sharedAdmission) lease(g *sharedGroup) func() {
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); defer s.mu.Unlock(); g.active--; s.active--; s.dispatch() }) }
}
func (s *sharedAdmission) dispatch() {
	for s.active < s.config.Concurrency && s.waiting > 0 {
		take := func(q []*sharedWaiter) (*sharedWaiter, []*sharedWaiter) {
			for i, w := range q {
				if w.group.active < w.group.limit {
					return w, append(q[:i], q[i+1:]...)
				}
			}
			return nil, q
		}
		var w *sharedWaiter
		preferBackground := len(s.background) > 0 && (len(s.foreground) == 0 || s.burst >= s.config.InteractiveBurst)
		if preferBackground {
			w, s.background = take(s.background)
		}
		if w == nil {
			w, s.foreground = take(s.foreground)
		}
		if w == nil {
			w, s.background = take(s.background)
		}
		if w == nil {
			return
		}
		if w.background {
			s.burst = 0
		} else if len(s.background) > 0 {
			s.burst++
		} else {
			s.burst = 0
		}
		s.waiting--
		s.active++
		w.group.waiting--
		w.group.active++
		w.granted = true
		close(w.ready)
	}
}

// SharedRuntimeSnapshot reports actual global occupancy and endpoint groups;
// hashed keys contain no endpoint credentials or model names.
type SharedRuntimeSnapshot struct {
	Enabled                            bool
	Active, Waiting, Limit, QueueLimit int
	Groups                             map[string][2]int // active, waiting
}

func SharedRuntimeStats() SharedRuntimeSnapshot {
	sharedGate.mu.Lock()
	defer sharedGate.mu.Unlock()
	out := SharedRuntimeSnapshot{Enabled: sharedGate.config.Enabled, Active: sharedGate.active, Waiting: sharedGate.waiting, Limit: sharedGate.config.Concurrency, QueueLimit: sharedGate.config.MaxWaiting, Groups: map[string][2]int{}}
	for key, g := range sharedGate.groups {
		out.Groups[key] = [2]int{g.active, g.waiting}
	}
	return out
}

// HasResourceBudget is consulted after a provider registers its endpoint.
func HasResourceBudget(modelID string) bool {
	sharedGate.mu.Lock()
	defer sharedGate.mu.Unlock()
	return sharedGate.config.Enabled && strings.HasPrefix(sharedGate.models[modelID], "resource-")
}
