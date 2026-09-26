// Package hostpool connects app nodes to standalone plugin hosts
// (weknora plugin-host) without Kubernetes: each host announces itself and
// the plugins it runs in Redis, and app nodes call the least busy host that
// runs the version they need, through the host's signed gateway.
package hostpool

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Heartbeat timing: a host refreshes its record every HeartbeatInterval and
// drops out HeartbeatTTL after its last one.
const (
	HeartbeatInterval = 5 * time.Second
	HeartbeatTTL      = 15 * time.Second
	// refreshAfter is how long an app node trusts its view of the hosts.
	refreshAfter = 2 * time.Second
)

const keyBase = "weknora:plugin-host:"

func keyPrefix() string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return keyBase + ns + ":"
	}
	return keyBase
}

// ErrNoClusterKey means neither SYSTEM_AES_KEY nor JWT_SECRET is set, so
// app nodes and plugin hosts share no secret to sign calls with.
var ErrNoClusterKey = errors.New("plugin hosts need SYSTEM_AES_KEY or JWT_SECRET, the same on every node")

// ClusterKey is the key app nodes sign gateway calls with, derived from the
// secrets every node already shares.
func ClusterKey() ([]byte, error) {
	var secret []byte
	switch {
	case utils.GetAESKey() != nil:
		secret = utils.GetAESKey()
	case strings.TrimSpace(os.Getenv("JWT_SECRET")) != "":
		secret = []byte(strings.TrimSpace(os.Getenv("JWT_SECRET")))
	default:
		return nil, ErrNoClusterKey
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("weknora plugin host gateway"))
	return mac.Sum(nil), nil
}

// Info is one plugin host's announcement.
type Info struct {
	ID  string `json:"id"`
	URL string `json:"url"` // gateway base URL, reachable from app nodes
	OS  string `json:"os"`
	// Arch and Kinds say what the host can run.
	Arch      string         `json:"arch"`
	Kinds     []string       `json:"kinds"`
	Plugins   []host.Running `json:"plugins"`
	InFlight  int64          `json:"inFlight"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// runs reports whether the host serves a plugin version right now.
func (i Info) runs(pluginID, version string) bool {
	for _, p := range i.Plugins {
		if p.ID == pluginID && p.Version == version && p.State == host.StateReady {
			return true
		}
	}
	return false
}

// Announcer keeps a plugin host's record in Redis.
type Announcer struct {
	rdb *redis.Client
	mgr *host.Manager
	id  string
	url string
}

// NewAnnouncer announces the host at url (its gateway, as app nodes reach
// it) under id.
func NewAnnouncer(rdb *redis.Client, mgr *host.Manager, id, url string) *Announcer {
	return &Announcer{rdb: rdb, mgr: mgr, id: id, url: strings.TrimSuffix(url, "/")}
}

func (a *Announcer) info() Info {
	return Info{
		ID: a.id, URL: a.url, OS: runtime.GOOS, Arch: runtime.GOARCH, Kinds: a.mgr.Kinds(),
		Plugins: a.mgr.Running(), InFlight: a.mgr.InFlight(), UpdatedAt: time.Now(),
	}
}

// Announce writes the record once.
func (a *Announcer) Announce(ctx context.Context) error {
	b, err := json.Marshal(a.info())
	if err != nil {
		return err
	}
	return a.rdb.Set(ctx, keyPrefix()+a.id, b, HeartbeatTTL).Err()
}

// Run announces every HeartbeatInterval until ctx ends, then withdraws the
// record so app nodes stop calling at once.
func (a *Announcer) Run(ctx context.Context) {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		if err := a.Announce(ctx); err != nil && ctx.Err() == nil {
			logger.Warnf(ctx, "[plugin-host] heartbeat: %v", err)
		}
		select {
		case <-ctx.Done():
			wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.rdb.Del(wctx, keyPrefix()+a.id).Err()
			cancel()
			return
		case <-t.C:
		}
	}
}

// Pool is an app node's view of the plugin hosts.
type Pool struct {
	rdb  *redis.Client
	key  []byte
	http *http.Client
	now  func() time.Time

	mu      sync.Mutex
	hosts   []Info
	fetched time.Time
	clients map[string]*client.Client
}

// NewPool reads plugin hosts from Redis and signs calls with key.
func NewPool(rdb *redis.Client, key []byte) *Pool {
	return &Pool{
		rdb: rdb, key: key, now: time.Now, clients: map[string]*client.Client{},
		// Calls are bounded by their context; syncs stream for long.
		http: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second}},
	}
}

// Hosts lists the live plugin hosts.
func (p *Pool) Hosts(ctx context.Context) ([]Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hosts != nil && p.now().Sub(p.fetched) < refreshAfter {
		return p.hosts, nil
	}
	hosts, err := p.fetch(ctx)
	if err != nil {
		if p.hosts != nil {
			logger.Warnf(ctx, "[plugin] read plugin hosts, keeping the last view: %v", err)
			return p.hosts, nil
		}
		return nil, err
	}
	p.hosts, p.fetched = hosts, p.now()
	return hosts, nil
}

func (p *Pool) fetch(ctx context.Context) ([]Info, error) {
	var keys []string
	iter := p.rdb.Scan(ctx, 0, keyPrefix()+"*", 100).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	hosts := []Info{}
	if len(keys) == 0 {
		return hosts, nil
	}
	vals, err := p.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}
		var info Info
		if json.Unmarshal([]byte(s), &info) == nil && info.URL != "" &&
			p.now().Sub(info.UpdatedAt) < HeartbeatTTL {
			hosts = append(hosts, info)
		}
	}
	return hosts, nil
}

// Client reaches a plugin version on the least busy host that runs it.
func (p *Pool) Client(ctx context.Context, pluginID, version string) (*client.Client, error) {
	hosts, err := p.Hosts(ctx)
	if err != nil {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "read plugin hosts: %v", err)
	}
	var best []Info
	for _, h := range hosts {
		if !h.runs(pluginID, version) {
			continue
		}
		switch {
		case len(best) == 0 || h.InFlight < best[0].InFlight:
			best = []Info{h}
		case h.InFlight == best[0].InFlight:
			best = append(best, h)
		}
	}
	if len(best) == 0 {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable,
			"no plugin host runs %s@%s; is weknora plugin-host up?", pluginID, version)
	}
	h := best[rand.IntN(len(best))] //nolint:gosec // load spreading, not security
	base := fmt.Sprintf("%s%s%s/%s", h.URL, host.GatewayPrefix, pluginID, version)
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.clients[base]
	if !ok {
		c = client.New(base, p.http, client.Signed(p.key))
		p.clients[base] = c
	}
	return c, nil
}

// Runs reports whether some live host runs a plugin version.
func (p *Pool) Runs(ctx context.Context, pluginID, version string) bool {
	hosts, err := p.Hosts(ctx)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if h.runs(pluginID, version) {
			return true
		}
	}
	return false
}
