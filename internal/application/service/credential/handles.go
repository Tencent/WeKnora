package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// HandleTTL bounds how long a submitted form can still be filled.
const HandleTTL = 15 * time.Minute

// handleSeqTTL keeps a session's counter long enough that handles in one
// conversation never repeat while any of them can still be alive.
const handleSeqTTL = 24 * time.Hour

const (
	sourceOnce  = "once"
	sourceVault = "vault"
)

// NewHandle describes the values one handle unlocks. With VaultItemID set the
// password is read from the vault at fill time and its Value is left empty.
type NewHandle struct {
	Owner       Owner
	SessionID   string
	Domains     []string
	VaultItemID string
	Fields      []types.SecretField
}

type recordField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

type handleRecord struct {
	TenantID    uint64        `json:"tenant_id"`
	UserID      string        `json:"user_id"`
	SessionID   string        `json:"session_id"`
	Domains     []string      `json:"domains"`
	Source      string        `json:"source"`
	VaultItemID string        `json:"vault_item_id,omitempty"`
	Fields      []recordField `json:"fields"`
}

// HandleStore keeps submitted values in Redis, encrypted, under a handle the
// agent can name but never read. A handle is not a bearer token: it resolves
// only for the session and user that created it.
type HandleStore struct {
	rdb    *redis.Client
	vault  *Vault
	prefix string
	ttl    time.Duration
}

// NewHandleStore returns nil without Redis: the feature has no in-process
// fallback, and the tools are not registered then.
func NewHandleStore(rdb *redis.Client, vault *Vault) *HandleStore {
	if rdb == nil || vault == nil {
		return nil
	}
	ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	if ns == "" {
		ns = "weknora"
	}
	return &HandleStore{rdb: rdb, vault: vault, prefix: ns + ":cred:", ttl: HandleTTL}
}

func (s *HandleStore) handleKey(sessionID, handle string) string {
	return s.prefix + "handle:" + sessionID + ":" + handle
}

// Create stores h and returns its handle.
func (s *HandleStore) Create(ctx context.Context, h NewHandle) (string, error) {
	key := utils.GetAESKey()
	if key == nil {
		return "", errUnavailable()
	}
	if h.SessionID == "" {
		return "", errors.New("a credential handle needs a session")
	}
	seqKey := s.prefix + "seq:" + h.SessionID
	seq, err := s.rdb.Incr(ctx, seqKey).Result()
	if err != nil {
		return "", fmt.Errorf("allocate credential handle: %w", err)
	}
	_ = s.rdb.Expire(ctx, seqKey, handleSeqTTL).Err()
	handle := fmt.Sprintf("cred_%d", seq)

	rec := handleRecord{
		TenantID: h.Owner.TenantID, UserID: h.Owner.UserID, SessionID: h.SessionID,
		Domains: h.Domains, Source: sourceOnce, VaultItemID: h.VaultItemID,
	}
	if h.VaultItemID != "" {
		rec.Source = sourceVault
	}
	for _, f := range h.Fields {
		rec.Fields = append(rec.Fields, recordField{Name: f.Name, Type: f.Type, Value: f.Value})
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	enc, err := utils.EncryptAESGCM(string(raw), key)
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, s.handleKey(h.SessionID, handle), enc, s.ttl).Err(); err != nil {
		return "", fmt.Errorf("store credential handle: %w", err)
	}
	return handle, nil
}

func (s *HandleStore) load(
	ctx context.Context, tenantID uint64, userID, sessionID, handle string,
) (*handleRecord, error) {
	if !types.ValidSecretHandle(handle) || sessionID == "" {
		return nil, types.ErrSecretHandleNotFound
	}
	enc, err := s.rdb.Get(ctx, s.handleKey(sessionID, handle)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, types.ErrSecretHandleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read credential handle: %w", err)
	}
	key := utils.GetAESKey()
	if key == nil || !strings.HasPrefix(enc, utils.EncPrefix) {
		return nil, types.ErrSecretHandleNotFound
	}
	raw, err := utils.DecryptAESGCM(enc, key)
	if err != nil {
		return nil, types.ErrSecretHandleNotFound
	}
	var rec handleRecord
	if json.Unmarshal([]byte(raw), &rec) != nil {
		return nil, types.ErrSecretHandleNotFound
	}
	if rec.SessionID != sessionID || rec.UserID != userID || rec.TenantID != tenantID {
		return nil, types.ErrSecretHandleNotFound
	}
	return &rec, nil
}

// Resolve returns the values behind handle for the given caller.
func (s *HandleStore) Resolve(
	ctx context.Context, tenantID uint64, userID, sessionID, handle string,
) (*types.ResolvedSecret, error) {
	rec, err := s.load(ctx, tenantID, userID, sessionID, handle)
	if err != nil {
		return nil, err
	}
	var password string
	if rec.Source == sourceVault {
		_, pw, err := s.vault.Password(ctx, Owner{TenantID: tenantID, UserID: userID}, rec.VaultItemID)
		if err != nil {
			return nil, types.ErrCredentialUnavailable
		}
		password = pw
	}
	out := &types.ResolvedSecret{Domains: rec.Domains, VaultItemID: rec.VaultItemID}
	for _, f := range rec.Fields {
		value := f.Value
		if rec.Source == sourceVault && f.Name == types.SecureFormFieldPassword {
			value = password
		}
		out.Fields = append(out.Fields, types.SecretField{Name: f.Name, Type: f.Type, Value: value})
	}
	return out, nil
}

// Complete drops a handle whose every field was filled and marks the saved
// login as used.
func (s *HandleStore) Complete(
	ctx context.Context, tenantID uint64, userID, sessionID, handle, vaultItemID string,
) error {
	if err := s.Discard(ctx, sessionID, handle); err != nil {
		return err
	}
	if vaultItemID != "" {
		return s.vault.Touch(ctx, Owner{TenantID: tenantID, UserID: userID}, vaultItemID)
	}
	return nil
}

// Discard drops a handle.
func (s *HandleStore) Discard(ctx context.Context, sessionID, handle string) error {
	if !types.ValidSecretHandle(handle) || sessionID == "" {
		return nil
	}
	return s.rdb.Del(ctx, s.handleKey(sessionID, handle)).Err()
}
