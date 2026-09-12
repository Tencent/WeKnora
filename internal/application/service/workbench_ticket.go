package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

// Workbench ticket and console bounds apply across all API replicas.
const (
	WorkbenchTicketTTL                    = 30 * time.Second
	WorkbenchMaxAuthenticatedConsoles     = 256
	WorkbenchMaxAuthenticatedConsolesUser = 4
	workbenchLeaseTTL                     = 15 * time.Second
	workbenchMemoryLimit                  = 2048
)

// WorkbenchIdentity binds a single-use ticket to its web user, session and origin.
type WorkbenchIdentity struct {
	TenantID  uint64    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	TokenID   string    `json:"token_id"`
	Origin    string    `json:"origin"`
	ExpiresAt time.Time `json:"expires_at"`
}

type workbenchTokenContextKey struct{}

// Context restores only the verified web identity needed for reauthorization.
func (i WorkbenchIdentity) Context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, i.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, i.UserID)
	ctx = types.WithSandboxTenantID(ctx, i.TenantID)
	if i.TokenID != "" {
		ctx = context.WithValue(ctx, workbenchTokenContextKey{}, i.TokenID)
	}
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: i.UserID})
}

// WorkbenchTicket is returned over authenticated HTTP and consumed in a socket frame.
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

// IssueTicket authorizes the session and stores only the random ticket's hash.
func (s *WorkbenchService) IssueTicket(
	ctx context.Context, sessionID, origin, accessToken string,
) (*WorkbenchTicket, error) {
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
	if s.deps.Users == nil {
		return nil, ErrWorkbenchUnavailable
	}
	uid, _ := types.UserIDFromContext(ctx)
	record, err := s.deps.Users.GetAccessTokenByValue(ctx, accessToken)
	if err != nil {
		if errors.Is(err, repository.ErrTokenNotFound) {
			return nil, ErrWorkbenchDenied
		}
		return nil, ErrWorkbenchUnavailable
	}
	if record == nil || strings.TrimSpace(record.ID) == "" ||
		AssertAccessTokenStillActive(record, uid, time.Now()) != nil {
		return nil, ErrWorkbenchDenied
	}
	token, err := workbenchRandomToken()
	if err != nil {
		return nil, err
	}
	tid, _ := types.TenantIDFromContext(ctx)
	identity := WorkbenchIdentity{
		TenantID: tid, UserID: uid, SessionID: sessionID,
		TokenID: record.ID,
		Origin:  origin, ExpiresAt: time.Now().Add(WorkbenchTicketTTL),
	}
	if err := s.store.putTicket(ctx, workbenchHash(token), identity); err != nil {
		return nil, err
	}
	return &WorkbenchTicket{token, int(WorkbenchTicketTTL.Seconds()), "/api/v1/sandbox-terminal"}, nil
}

// ConsumeTicket atomically consumes a ticket and rechecks its current permissions.
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
	if err := s.checkTerminalToken(ctx, i.TokenID, i.UserID, true); err != nil {
		return WorkbenchIdentity{}, err
	}
	if _, err := s.Authorize(i.Context(ctx), i.SessionID); err != nil {
		return WorkbenchIdentity{}, err
	}
	return i, nil
}

func (s *WorkbenchService) checkTerminalToken(ctx context.Context, tokenID, userID string, rejectExpired bool) error {
	if strings.TrimSpace(tokenID) == "" {
		return ErrWorkbenchDenied
	}
	if s.deps.Users == nil {
		return ErrWorkbenchUnavailable
	}
	token, err := s.deps.Users.GetAccessTokenByID(ctx, tokenID)
	if err != nil {
		if errors.Is(err, repository.ErrTokenNotFound) {
			return ErrWorkbenchDenied
		}
		return ErrWorkbenchUnavailable
	}
	if AssertAccessTokenNotRevoked(token, userID) != nil {
		return ErrWorkbenchDenied
	}
	if rejectExpired && assertAccessTokenNotExpired(token, time.Now()) != nil {
		return ErrWorkbenchDenied
	}
	return nil
}

type workbenchStore interface {
	putTicket(context.Context, string, WorkbenchIdentity) error
	consumeTicket(context.Context, string) (WorkbenchIdentity, error)
	acquire(context.Context, string, string, string) error
	renew(context.Context, string, string, string) error
	release(context.Context, string, string, string) error
}

// WorkbenchLease holds deployment, user and session console slots under one token.
type WorkbenchLease struct {
	store               workbenchStore
	sessionKey, userKey string
	token               string
}

// AcquireConsole reauthorizes the identity before claiming bounded console slots.
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
	sessionKey := workbenchHash(fmt.Sprintf("%d:%s", i.TenantID, i.SessionID))
	userKey := workbenchHash(i.UserID)
	if err := s.store.acquire(ctx, sessionKey, userKey, token); err != nil {
		return nil, err
	}
	return &WorkbenchLease{s.store, sessionKey, userKey, token}, nil
}

// Renew extends the slots only while this lease token still owns them.
func (l *WorkbenchLease) Renew(ctx context.Context) error {
	return l.store.renew(ctx, l.sessionKey, l.userKey, l.token)
}

// Release deletes only the slots still owned by this lease token.
func (l *WorkbenchLease) Release(ctx context.Context) error {
	return l.store.release(ctx, l.sessionKey, l.userKey, l.token)
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

var workbenchAcquireScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local expires_at = now_ms + tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now_ms)
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now_ms)
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
if redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[3]) then return -1 end
if redis.call('ZCARD', KEYS[3]) >= tonumber(ARGV[4]) then return -1 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('ZADD', KEYS[2], expires_at, ARGV[1])
redis.call('ZADD', KEYS[3], expires_at, ARGV[1])
redis.call('PEXPIRE', KEYS[2], ARGV[2] * 2)
redis.call('PEXPIRE', KEYS[3], ARGV[2] * 2)
return 1
`)

func (s *redisWorkbenchStore) consoleKeys(sessionKey, userKey string) []string {
	base := s.prefix + "{console}:"
	return []string{base + "session:" + sessionKey, base + "global", base + "user:" + userKey}
}

func (s *redisWorkbenchStore) acquire(ctx context.Context, sessionKey, userKey, token string) error {
	n, err := workbenchAcquireScript.Run(ctx, s.client, s.consoleKeys(sessionKey, userKey),
		token, workbenchLeaseTTL.Milliseconds(),
		WorkbenchMaxAuthenticatedConsoles, WorkbenchMaxAuthenticatedConsolesUser).Int()
	if err != nil {
		return ErrWorkbenchUnavailable
	}
	if n != 1 {
		return ErrWorkbenchBusy
	}
	return nil
}

var workbenchRenewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if not redis.call('ZSCORE', KEYS[2], ARGV[1]) or not redis.call('ZSCORE', KEYS[3], ARGV[1]) then return 0 end
local now = redis.call('TIME')
local expires_at = now[1] * 1000 + math.floor(now[2] / 1000) + tonumber(ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('ZADD', KEYS[2], expires_at, ARGV[1])
redis.call('ZADD', KEYS[3], expires_at, ARGV[1])
redis.call('PEXPIRE', KEYS[2], ARGV[2] * 2)
redis.call('PEXPIRE', KEYS[3], ARGV[2] * 2)
return 1
`)

var workbenchReleaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[3], ARGV[1])
return 1
`)

func (s *redisWorkbenchStore) renew(ctx context.Context, sessionKey, userKey, token string) error {
	n, err := workbenchRenewScript.Run(ctx, s.client, s.consoleKeys(sessionKey, userKey),
		token, workbenchLeaseTTL.Milliseconds()).Int()
	if err != nil || n != 1 {
		return ErrWorkbenchUnavailable
	}
	return nil
}

func (s *redisWorkbenchStore) release(ctx context.Context, sessionKey, userKey, token string) error {
	_, err := workbenchReleaseScript.Run(ctx, s.client, s.consoleKeys(sessionKey, userKey), token).Result()
	if err != nil {
		return ErrWorkbenchUnavailable
	}
	return nil
}

type workbenchMemoryLease struct {
	token   string
	userKey string
	expires time.Time
}
type memoryWorkbenchStore struct {
	mu      sync.Mutex
	tickets map[string]WorkbenchIdentity
	leases  map[string]workbenchMemoryLease
	now     func() time.Time
}

func newMemoryWorkbenchStore() *memoryWorkbenchStore {
	return &memoryWorkbenchStore{
		tickets: make(map[string]WorkbenchIdentity),
		leases:  make(map[string]workbenchMemoryLease),
		now:     time.Now,
	}
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

func (s *memoryWorkbenchStore) acquire(ctx context.Context, sessionKey, userKey, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	if _, ok := s.leases[sessionKey]; ok {
		return ErrWorkbenchBusy
	}
	if len(s.leases) >= WorkbenchMaxAuthenticatedConsoles {
		return ErrWorkbenchBusy
	}
	userConsoles := 0
	for _, lease := range s.leases {
		if lease.userKey == userKey {
			userConsoles++
		}
	}
	if userConsoles >= WorkbenchMaxAuthenticatedConsolesUser {
		return ErrWorkbenchBusy
	}
	s.leases[sessionKey] = workbenchMemoryLease{token: token, userKey: userKey, expires: s.now().Add(workbenchLeaseTTL)}
	return nil
}

func (s *memoryWorkbenchStore) renew(ctx context.Context, sessionKey, userKey, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	l, ok := s.leases[sessionKey]
	if !ok || l.token != token || l.userKey != userKey {
		return ErrWorkbenchUnavailable
	}
	l.expires = s.now().Add(workbenchLeaseTTL)
	s.leases[sessionKey] = l
	return nil
}

func (s *memoryWorkbenchStore) release(ctx context.Context, sessionKey, userKey, token string) error {
	if ctx.Err() != nil {
		return ErrWorkbenchUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[sessionKey]
	if ok && lease.token == token && lease.userKey == userKey {
		delete(s.leases, sessionKey)
	}
	return nil
}
