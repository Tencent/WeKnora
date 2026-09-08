package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	WorkbenchTicketTTL   = 30 * time.Second
	workbenchLeaseTTL    = 15 * time.Second
	workbenchMemoryLimit = 2048
)

type WorkbenchIdentity struct {
	TenantID  uint64    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	Origin    string    `json:"origin"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (i WorkbenchIdentity) Context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, i.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, i.UserID)
	ctx = types.WithSandboxTenantID(ctx, i.TenantID)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: i.UserID})
}

type WorkbenchTicket struct {
	Ticket        string `json:"ticket"`
	ExpiresIn     int    `json:"expires_in"`
	WebsocketPath string `json:"websocket_path"`
}

func workbenchRandomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrWorkbenchUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func workbenchHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *WorkbenchService) IssueTicket(ctx context.Context, sessionID, origin string) (*WorkbenchTicket, error) {
	status, err := s.Status(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !status.Capabilities["terminal"] {
		return nil, ErrWorkbenchCapability
	}
	if s.store == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if origin == "" {
		return nil, ErrWorkbenchInvalid
	}
	token, err := workbenchRandomToken()
	if err != nil {
		return nil, err
	}
	tid, _ := types.TenantIDFromContext(ctx)
	uid, _ := types.UserIDFromContext(ctx)
	identity := WorkbenchIdentity{tid, uid, sessionID, origin, time.Now().Add(WorkbenchTicketTTL)}
	if err := s.store.putTicket(ctx, workbenchHash(token), identity); err != nil {
		return nil, err
	}
	return &WorkbenchTicket{token, int(WorkbenchTicketTTL.Seconds()), "/api/v1/sandbox-terminal"}, nil
}

func (s *WorkbenchService) ConsumeTicket(ctx context.Context, ticket, origin string) (WorkbenchIdentity, error) {
	if !s.Enabled() {
		return WorkbenchIdentity{}, ErrWorkbenchDisabled
	}
	if s.store == nil {
		return WorkbenchIdentity{}, ErrWorkbenchUnavailable
	}
	if len(ticket) != 43 {
		return WorkbenchIdentity{}, ErrWorkbenchTicket
	}
	i, err := s.store.consumeTicket(ctx, workbenchHash(ticket))
	if err != nil {
		return WorkbenchIdentity{}, err
	}
	if i.Origin != origin || origin == "" || !time.Now().Before(i.ExpiresAt) || i.UserID == "" || i.TenantID == 0 {
		return WorkbenchIdentity{}, ErrWorkbenchTicket
	}
	if _, err := s.Authorize(i.Context(ctx), i.SessionID); err != nil {
		return WorkbenchIdentity{}, err
	}
	return i, nil
}

type workbenchStore interface {
	putTicket(context.Context, string, WorkbenchIdentity) error
	consumeTicket(context.Context, string) (WorkbenchIdentity, error)
	acquire(context.Context, string, string) error
	renew(context.Context, string, string) error
	release(context.Context, string, string) error
}

type WorkbenchLease struct {
	store      workbenchStore
	key, token string
}

func (s *WorkbenchService) AcquireConsole(ctx context.Context, i WorkbenchIdentity) (*WorkbenchLease, error) {
	if s.store == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if _, err := s.Authorize(i.Context(ctx), i.SessionID); err != nil {
		return nil, err
	}
	token, err := workbenchRandomToken()
	if err != nil {
		return nil, err
	}
	key := workbenchHash(fmt.Sprintf("%d:%s", i.TenantID, i.SessionID))
	if err := s.store.acquire(ctx, key, token); err != nil {
		return nil, err
	}
	return &WorkbenchLease{s.store, key, token}, nil
}

func (l *WorkbenchLease) Renew(ctx context.Context) error { return l.store.renew(ctx, l.key, l.token) }
func (l *WorkbenchLease) Release(ctx context.Context) error {
	return l.store.release(ctx, l.key, l.token)
}

type redisWorkbenchStore struct {
	client *redis.Client
	prefix string
}

func (s *redisWorkbenchStore) putTicket(ctx context.Context, hash string, i WorkbenchIdentity) error {
	data, err := json.Marshal(i)
	if err != nil {
		return ErrWorkbenchUnavailable
	}
	ok, err := s.client.SetNX(ctx, s.prefix+"ticket:"+hash, data, WorkbenchTicketTTL).Result()
	if err != nil || !ok {
		return ErrWorkbenchUnavailable
	}
	return nil
}

func (s *redisWorkbenchStore) consumeTicket(ctx context.Context, hash string) (WorkbenchIdentity, error) {
	// GETDEL is atomic across replicas. The plaintext ticket is never stored.
	data, err := s.client.GetDel(ctx, s.prefix+"ticket:"+hash).Bytes()
	if err == redis.Nil {
		return WorkbenchIdentity{}, ErrWorkbenchTicket
	}
	if err != nil {
		return WorkbenchIdentity{}, ErrWorkbenchUnavailable
	}
	var i WorkbenchIdentity
	if json.Unmarshal(data, &i) != nil {
		return WorkbenchIdentity{}, ErrWorkbenchTicket
	}
	return i, nil
}

func (s *redisWorkbenchStore) acquire(ctx context.Context, key, token string) error {
	ok, err := s.client.SetNX(ctx, s.prefix+"console:"+key, token, workbenchLeaseTTL).Result()
	if err != nil {
		return ErrWorkbenchUnavailable
	}
	if !ok {
		return ErrWorkbenchBusy
	}
	return nil
}

var workbenchRenewScript = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end return 0`)
var workbenchReleaseScript = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`)

func (s *redisWorkbenchStore) renew(ctx context.Context, key, token string) error {
	n, err := workbenchRenewScript.Run(ctx, s.client, []string{s.prefix + "console:" + key}, token, workbenchLeaseTTL.Milliseconds()).Int()
	if err != nil || n != 1 {
		return ErrWorkbenchUnavailable
	}
	return nil
}

func (s *redisWorkbenchStore) release(ctx context.Context, key, token string) error {
	if _, err := workbenchReleaseScript.Run(ctx, s.client, []string{s.prefix + "console:" + key}, token).Result(); err != nil {
		return ErrWorkbenchUnavailable
	}
	return nil
}

type workbenchMemoryLease struct {
	token   string
	expires time.Time
}
type memoryWorkbenchStore struct {
	mu      sync.Mutex
	tickets map[string]WorkbenchIdentity
	leases  map[string]workbenchMemoryLease
	now     func() time.Time
}

func newMemoryWorkbenchStore() *memoryWorkbenchStore {
	return &memoryWorkbenchStore{tickets: make(map[string]WorkbenchIdentity), leases: make(map[string]workbenchMemoryLease), now: time.Now}
}

func (s *memoryWorkbenchStore) purge() {
	now := s.now()
	for key, i := range s.tickets {
		if !now.Before(i.ExpiresAt) {
			delete(s.tickets, key)
		}
	}
	for key, l := range s.leases {
		if !now.Before(l.expires) {
			delete(s.leases, key)
		}
	}
}

func (s *memoryWorkbenchStore) putTicket(ctx context.Context, hash string, i WorkbenchIdentity) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	if len(s.tickets) >= workbenchMemoryLimit {
		return ErrWorkbenchBusy
	}
	if _, exists := s.tickets[hash]; exists {
		return ErrWorkbenchUnavailable
	}
	s.tickets[hash] = i
	return nil
}

func (s *memoryWorkbenchStore) consumeTicket(ctx context.Context, hash string) (WorkbenchIdentity, error) {
	if ctx.Err() != nil {
		return WorkbenchIdentity{}, ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	i, ok := s.tickets[hash]
	delete(s.tickets, hash)
	if !ok {
		return WorkbenchIdentity{}, ErrWorkbenchTicket
	}
	return i, nil
}

func (s *memoryWorkbenchStore) acquire(ctx context.Context, key, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	if _, ok := s.leases[key]; ok {
		return ErrWorkbenchBusy
	}
	if len(s.leases) >= workbenchMemoryLimit {
		return ErrWorkbenchBusy
	}
	s.leases[key] = workbenchMemoryLease{token, s.now().Add(workbenchLeaseTTL)}
	return nil
}

func (s *memoryWorkbenchStore) renew(ctx context.Context, key, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	l, ok := s.leases[key]
	if !ok || l.token != token {
		return ErrWorkbenchUnavailable
	}
	s.leases[key] = workbenchMemoryLease{token, s.now().Add(workbenchLeaseTTL)}
	return nil
}

func (s *memoryWorkbenchStore) release(ctx context.Context, key, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[key].token == token {
		delete(s.leases, key)
	}
	return nil
}
