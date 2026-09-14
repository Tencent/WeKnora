package browserskill

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrAuthorization indicates an invalid, expired or revoked browser grant.
var ErrAuthorization = errors.New("browser authorization is invalid or expired")

// ErrLeaseHeld indicates that another replica still owns the device connection.
var ErrLeaseHeld = errors.New("browser connection is owned by another node; reconnect shortly")

const (
	pairingLifetime = 5 * time.Minute
	deviceLifetime  = 90 * 24 * time.Hour
	renewalInterval = 30 * 24 * time.Hour
	leaseLifetime   = 45 * time.Second
)

// DeviceRecord stores authorization metadata and SHA-256 token hashes, never secrets.
// One registered device per tenant/user remains the current product contract.
type DeviceRecord struct {
	ScopeKey      string     `gorm:"primaryKey;size:32"  json:"-"`
	ID            string     `gorm:"uniqueIndex;size:32" json:"id"`
	Tenant        uint64     `                           json:"-"`
	User          string     `gorm:"size:36"             json:"-"`
	Label         string     `gorm:"size:100"            json:"label"`
	TokenHash     string     `gorm:"uniqueIndex;size:64" json:"-"`
	PreviousHash  string     `gorm:"size:64"             json:"-"`
	PreviousUntil time.Time  `                           json:"-"`
	ExpiresAt     time.Time  `                           json:"expires_at"`
	RenewAfter    time.Time  `                           json:"renew_after"`
	CreatedAt     time.Time  `                           json:"created_at"`
	LastSeenAt    time.Time  `                           json:"last_seen_at"`
	RevokedAt     *time.Time `                           json:"revoked_at,omitempty"`
	Owner         string     `gorm:"size:32"             json:"-"`
	LeaseKey      string     `gorm:"size:32"             json:"-"`
	OwnerURL      string     `gorm:"size:500"            json:"-"`
	LeaseUntil    time.Time  `                           json:"-"`
}

// TableName selects the durable browser device table.
func (DeviceRecord) TableName() string { return "browser_devices" }
func (r DeviceRecord) scope() Scope    { return Scope{r.Tenant, r.User} }

// PairingRecord stores a short-lived, single-use pairing token hash.
type PairingRecord struct {
	ScopeKey  string `gorm:"primaryKey;size:32"`
	TokenHash string `gorm:"uniqueIndex;size:64"`
	Tenant    uint64
	User      string `gorm:"size:36"`
	ExpiresAt time.Time
}

// TableName selects the one-use browser pairing table.
func (PairingRecord) TableName() string { return "browser_pairings" }

// TaskRecord marks tasks that require explicit resume after interruption.
type TaskRecord struct {
	ScopeKey string `gorm:"primaryKey;size:32"`
	Session  string `gorm:"primaryKey;size:36"`
}

// TableName selects the durable task interruption table.
func (TaskRecord) TableName() string { return "browser_task_interruptions" }

// Store also checks the current user/membership, so suspended accounts cannot
// continue using a previously issued device credential.
type Store struct {
	db          *gorm.DB
	checkMember bool
}

// NewStore enables durable authorization with current membership checks.
func NewStore(db *gorm.DB) *Store { return &Store{db: db, checkMember: true} }

func (s *Store) member(ctx context.Context, scope Scope) error {
	if !s.checkMember {
		return nil
	}
	var count int64
	err := s.db.WithContext(ctx).
		Table("users AS u").
		Where("u.id = ? AND u.is_active = ? AND u.deleted_at IS NULL", scope.User, true).
		Where("(u.can_access_all_tenants = ? AND u.tenant_id <> ?) OR EXISTS (SELECT 1 FROM tenant_members "+
			"AS m WHERE m.user_id = u.id AND m.tenant_id = ? AND m.status = ? AND m.deleted_at IS NULL)",
			true, scope.Tenant, scope.Tenant, "active").
		Count(&count).
		Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrAuthorization
	}
	return nil
}

func (s *Store) createPair(ctx context.Context, p PairingRecord) error {
	if err := s.member(ctx, Scope{p.Tenant, p.User}); err != nil {
		return err
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope_key"}}, UpdateAll: true}).
		Create(&p).
		Error
}

func (s *Store) exchange(ctx context.Context, oldHash string, r DeviceRecord) (*DeviceRecord, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p PairingRecord
		if err := tx.Where("token_hash = ? AND expires_at > ?", oldHash, time.Now()).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthorization
			}
			return err
		}
		if err := (&Store{db: tx, checkMember: s.checkMember}).member(ctx, Scope{p.Tenant, p.User}); err != nil {
			return err
		}
		used := tx.Where("scope_key = ? AND token_hash = ? AND expires_at > ?", p.ScopeKey, oldHash, time.Now()).
			Delete(&PairingRecord{})
		if used.Error != nil {
			return used.Error
		}
		if used.RowsAffected != 1 {
			return ErrAuthorization
		}
		r.ScopeKey, r.Tenant, r.User = p.ScopeKey, p.Tenant, p.User
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope_key"}}, UpdateAll: true}).
			Create(&r).
			Error
	})
	return &r, err
}

func (s *Store) account(ctx context.Context, scope Scope) (*DeviceRecord, error) {
	var r DeviceRecord
	err := s.db.WithContext(ctx).Where("scope_key = ?", scope.key()).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) authenticate(ctx context.Context, hash string) (*DeviceRecord, error) {
	var r DeviceRecord
	now := time.Now()
	err := s.db.WithContext(ctx).
		Where("revoked_at IS NULL AND expires_at > ? AND (token_hash = ? OR (previous_hash = ? AND "+
			"previous_until > ?))", now, hash, hash, now).
		First(&r).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAuthorization
	}
	if err != nil {
		return nil, err
	}
	if err = s.member(ctx, r.scope()); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) renew(ctx context.Context, oldHash, nextHash string) (*DeviceRecord, error) {
	// A persisted client-generated candidate makes lost responses retryable.
	// Possession of the new token proves ownership even after the old grace expires.
	r, err := s.authenticate(ctx, nextHash)
	if err == nil && r.TokenHash == nextHash && r.PreviousHash == oldHash {
		return r, nil
	}
	r, err = s.authenticate(ctx, oldHash)
	if err != nil {
		return nil, err
	}
	if r.TokenHash != oldHash {
		return nil, ErrAuthorization
	}
	now := time.Now()
	result := s.db.WithContext(ctx).
		Model(&DeviceRecord{}).
		Where("id = ? AND token_hash = ? AND revoked_at IS NULL", r.ID, oldHash).
		Updates(map[string]any{
			"token_hash": nextHash, "previous_hash": oldHash, "previous_until": now.Add(5 * time.Minute),
			"expires_at": now.Add(deviceLifetime), "renew_after": now.Add(renewalInterval),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrAuthorization
	}
	return s.authenticate(ctx, nextHash)
}

func (s *Store) revoke(ctx context.Context, scope Scope) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scope_key = ?", scope.key()).Delete(&PairingRecord{}).Error; err != nil {
			return err
		}
		return tx.Model(&DeviceRecord{}).
			Where("scope_key = ?", scope.key()).
			Updates(map[string]any{"revoked_at": time.Now(), "owner": "", "owner_url": "", "lease_until": time.Time{}}).
			Error
	})
}

func (s *Store) claim(ctx context.Context, r *DeviceRecord, owner, url, leaseKey string) error {
	now := time.Now()
	result := s.db.WithContext(ctx).
		Model(&DeviceRecord{}).
		Where("id = ? AND revoked_at IS NULL AND expires_at > ? AND (lease_until < ? OR owner = '')", r.ID, now, now).
		Updates(map[string]any{
			"owner": owner, "owner_url": url, "lease_key": leaseKey,
			"lease_until": now.Add(leaseLifetime), "last_seen_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLeaseHeld
	}
	return nil
}

func (s *Store) heartbeat(ctx context.Context, id, leaseKey string) (*DeviceRecord, error) {
	var r DeviceRecord
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND lease_key = ? AND revoked_at IS NULL AND expires_at > ?", id, leaseKey, time.Now(),
	).First(&r).Error; err != nil {
		return nil, err
	}
	if err := s.member(ctx, r.scope()); err != nil {
		return nil, err
	}
	result := s.db.WithContext(ctx).
		Model(&DeviceRecord{}).
		Where("id = ? AND lease_key = ? AND revoked_at IS NULL AND lease_until > ?", id, leaseKey, time.Now()).
		Updates(map[string]any{"lease_until": time.Now().Add(leaseLifetime), "last_seen_at": time.Now()})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrAuthorization
	}
	return &r, nil
}

func (s *Store) release(ctx context.Context, id, leaseKey string) error {
	return s.db.WithContext(ctx).
		Model(&DeviceRecord{}).
		Where("id = ? AND lease_key = ?", id, leaseKey).
		Updates(map[string]any{"owner": "", "owner_url": "", "lease_until": time.Time{}}).
		Error
}

func (s *Store) tasks(ctx context.Context, scope Scope) ([]TaskRecord, error) {
	var rows []TaskRecord
	err := s.db.WithContext(ctx).Where("scope_key = ?", scope.key()).Find(&rows).Error
	return rows, err
}

func (s *Store) markTask(ctx context.Context, scope Scope, session string) error {
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&TaskRecord{scope.key(), session}).
		Error
}

func (s *Store) clearTask(ctx context.Context, scope Scope, session string) error {
	return s.db.WithContext(ctx).
		Where("scope_key = ? AND session = ?", scope.key(), session).
		Delete(&TaskRecord{}).
		Error
}

func (s *Store) releaseOwner(ctx context.Context, owner string) error {
	return s.db.WithContext(ctx).
		Model(&DeviceRecord{}).
		Where("owner = ?", owner).
		Updates(map[string]any{"owner": "", "owner_url": "", "lease_until": time.Time{}}).
		Error
}
