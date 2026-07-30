package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func artifactTestDB(t *testing.T) *gorm.DB {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "artifacts.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	return db
}

func claimInput(tenant uint64, key, owner string, now time.Time) interfaces.ArtifactClaim {
	return interfaces.ArtifactClaim{TenantID: tenant, ArtifactKey: key, ArtifactKind: "summary", InputDigest: artifactkey.DigestText("input"), ProducerVersion: "v1", OwnerToken: owner, LeaseDuration: time.Minute, Now: now}
}

func completionInput(tenant uint64, key, owner string, payload []byte, now time.Time) interfaces.ArtifactCompletion {
	return interfaces.ArtifactCompletion{TenantID: tenant, ArtifactKey: key, OwnerToken: owner, Payload: payload, PayloadEncoding: "json", PayloadDigest: artifactkey.DigestBytes(payload), CompletedAt: now}
}

func TestDerivedArtifactRepositoryLifecycleAndTenantIsolation(t *testing.T) {
	db := artifactTestDB(t)
	repo := NewDerivedArtifactRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	key := artifactkey.Generate(artifactkey.KeyInput{Kind: "summary", InputDigest: artifactkey.DigestText("input"), ProducerVersion: "v1"})
	_, err := repo.GetSucceeded(ctx, 1, key)
	require.ErrorIs(t, err, interfaces.ErrArtifactNotFound)
	claimed, err := repo.Claim(ctx, claimInput(1, key, "owner-a", now))
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimClaimed, claimed.Outcome)
	busy, err := repo.Claim(ctx, claimInput(1, key, "owner-b", now))
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimBusy, busy.Outcome)
	require.NoError(t, repo.RenewLease(ctx, 1, key, "owner-a", now.Add(10*time.Second), 2*time.Minute))
	payload := []byte(`{"summary":"ok"}`)
	digest := artifactkey.DigestBytes(payload)
	require.NoError(t, repo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: 1, ArtifactKey: key, OwnerToken: "owner-a", Payload: payload, PayloadEncoding: "json", PayloadDigest: digest, CompletedAt: now.Add(20 * time.Second)}))
	require.NoError(t, repo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: 1, ArtifactKey: key, OwnerToken: "owner-a", Payload: payload, PayloadEncoding: "json", PayloadDigest: digest, CompletedAt: now.Add(20 * time.Second)}))
	hit, err := repo.Claim(ctx, claimInput(1, key, "owner-c", now.Add(time.Hour)))
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimHit, hit.Outcome)
	_, err = repo.GetSucceeded(ctx, 2, key)
	require.ErrorIs(t, err, interfaces.ErrArtifactNotFound)
	other, err := repo.Claim(ctx, claimInput(2, key, "tenant-2", now))
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimClaimed, other.Outcome)
}

func TestDerivedArtifactLeaseTakeoverFailureAndRetry(t *testing.T) {
	repo := NewDerivedArtifactRepository(artifactTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("takeover")
	_, err := repo.Claim(ctx, claimInput(1, key, "old", now))
	require.NoError(t, err)
	takeover, err := repo.Claim(ctx, claimInput(1, key, "new", now.Add(2*time.Minute)))
	require.NoError(t, err)
	require.True(t, takeover.LeaseTakeover)
	require.Equal(t, 2, takeover.Artifact.AttemptCount)
	err = repo.Complete(ctx, completionInput(1, key, "old", []byte(`{"stale":true}`), now.Add(2*time.Minute)))
	require.ErrorIs(t, err, interfaces.ErrArtifactLostOwnership)
	err = repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: key, OwnerToken: "old", ErrorMessage: "stale", FailedAt: now.Add(2 * time.Minute)})
	require.ErrorIs(t, err, interfaces.ErrArtifactLostOwnership)
	err = repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: key, OwnerToken: "new", ErrorCode: "provider", ErrorMessage: string(make([]byte, 3000)), FailedAt: now.Add(2*time.Minute + time.Second)})
	require.NoError(t, err)
	retry, err := repo.Claim(ctx, claimInput(1, key, "retry", now.Add(3*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimClaimed, retry.Outcome)
	require.Equal(t, 3, retry.Artifact.AttemptCount)
}

func TestDerivedArtifactExpiredLeaseAndRenewalFencing(t *testing.T) {
	db := artifactTestDB(t)
	repo := NewDerivedArtifactRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("expiry")
	_, err := repo.Claim(ctx, claimInput(1, key, "owner", now))
	require.NoError(t, err)
	require.ErrorIs(t, repo.RenewLease(ctx, 1, key, "wrong", now.Add(time.Second), 2*time.Minute), interfaces.ErrArtifactLostOwnership)

	// A shorter requested lease is a successful no-op.
	require.NoError(t, repo.RenewLease(ctx, 1, key, "owner", now.Add(10*time.Second), 5*time.Second))
	var row types.DerivedArtifact
	require.NoError(t, db.Where("tenant_id = ? AND artifact_key = ?", 1, key).First(&row).Error)
	require.Equal(t, now.Add(time.Minute), row.LeaseExpiresAt.UTC())

	expired := now.Add(time.Minute + time.Second)
	require.ErrorIs(t, repo.Complete(ctx, completionInput(1, key, "owner", []byte(`{"late":true}`), expired)), interfaces.ErrArtifactLostOwnership)
	require.ErrorIs(t, repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: key, OwnerToken: "owner", FailedAt: expired}), interfaces.ErrArtifactLostOwnership)
	require.ErrorIs(t, repo.RenewLease(ctx, 1, key, "owner", expired, time.Minute), interfaces.ErrArtifactLostOwnership)
}

func TestDerivedArtifactCompletionValidationAndIdempotency(t *testing.T) {
	repo := NewDerivedArtifactRepository(artifactTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("completion")
	_, err := repo.Claim(ctx, claimInput(1, key, "owner", now))
	require.NoError(t, err)
	require.ErrorIs(t, repo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: 1, ArtifactKey: key, OwnerToken: "owner", CompletedAt: now.Add(time.Second)}), interfaces.ErrArtifactInvalidResult)
	bad := completionInput(1, key, "owner", []byte("good"), now.Add(time.Second))
	bad.PayloadDigest = artifactkey.DigestText("bad")
	require.ErrorIs(t, repo.Complete(ctx, bad), interfaces.ErrArtifactInvalidResult)
	missingDigest := completionInput(1, key, "owner", []byte("good"), now.Add(time.Second))
	missingDigest.PayloadDigest = ""
	require.ErrorIs(t, repo.Complete(ctx, missingDigest), interfaces.ErrArtifactInvalidResult)
	require.ErrorIs(t, repo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: 1, ArtifactKey: key, OwnerToken: "owner", ObjectURI: "object://future/result", CompletedAt: now.Add(time.Second)}), interfaces.ErrArtifactInvalidResult)

	good := completionInput(1, key, "owner", []byte("good"), now.Add(time.Second))
	require.NoError(t, repo.Complete(ctx, good))
	require.NoError(t, repo.Complete(ctx, good))
	differentPayload := completionInput(1, key, "owner", []byte("different"), now.Add(time.Second))
	require.ErrorIs(t, repo.Complete(ctx, differentPayload), interfaces.ErrArtifactLostOwnership)
	differentResult := interfaces.ArtifactCompletion{TenantID: 1, ArtifactKey: key, OwnerToken: "owner", ObjectURI: "object://future/result", PayloadDigest: artifactkey.DigestText("remote-object"), CompletedAt: now.Add(time.Second)}
	require.ErrorIs(t, repo.Complete(ctx, differentResult), interfaces.ErrArtifactLostOwnership)
}

func TestDerivedArtifactCorruptSucceededArtifactIsNeverHit(t *testing.T) {
	db := artifactTestDB(t)
	repo := NewDerivedArtifactRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("corrupt")
	_, err := repo.Claim(ctx, claimInput(1, key, "owner", now))
	require.NoError(t, err)
	require.NoError(t, repo.Complete(ctx, completionInput(1, key, "owner", []byte("original"), now.Add(time.Second))))
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("tenant_id = ? AND artifact_key = ?", 1, key).Update("payload", []byte("tampered")).Error)
	_, err = repo.GetSucceeded(ctx, 1, key)
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
	_, err = repo.Claim(ctx, claimInput(1, key, "new-owner", now.Add(time.Hour)))
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
	var row types.DerivedArtifact
	require.NoError(t, db.Where("tenant_id = ? AND artifact_key = ?", 1, key).First(&row).Error)
	require.Equal(t, types.DerivedArtifactSucceeded, row.Status)
}

func TestDerivedArtifactTenantOwnershipIsolation(t *testing.T) {
	repo := NewDerivedArtifactRepository(artifactTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("tenant-fence")
	_, err := repo.Claim(ctx, claimInput(2, key, "tenant-b", now))
	require.NoError(t, err)
	_, err = repo.GetSucceeded(ctx, 1, key)
	require.ErrorIs(t, err, interfaces.ErrArtifactNotFound)
	require.ErrorIs(t, repo.Complete(ctx, completionInput(1, key, "tenant-b", []byte("x"), now.Add(time.Second))), interfaces.ErrArtifactLostOwnership)
	require.ErrorIs(t, repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: key, OwnerToken: "tenant-b", FailedAt: now.Add(time.Second)}), interfaces.ErrArtifactLostOwnership)
	require.ErrorIs(t, repo.RenewLease(ctx, 1, key, "tenant-b", now.Add(time.Second), time.Minute), interfaces.ErrArtifactLostOwnership)
}

func TestDerivedArtifactFailureTruncation(t *testing.T) {
	db := artifactTestDB(t)
	repo := NewDerivedArtifactRepository(db)
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("failure-length")
	_, err := repo.Claim(ctx, claimInput(1, key, "owner", now))
	require.NoError(t, err)
	require.NoError(t, repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: key, OwnerToken: "owner", ErrorCode: "provider", ErrorMessage: string([]rune{'错'}) + string(make([]rune, 2200)), FailedAt: now.Add(time.Second)}))
	var row types.DerivedArtifact
	require.NoError(t, db.Where("tenant_id = ? AND artifact_key = ?", 1, key).First(&row).Error)
	require.LessOrEqual(t, len([]rune(row.ErrorMessage)), maxArtifactErrorMessage)
}

func TestDerivedArtifactMalformedKeyRejectedByWrites(t *testing.T) {
	repo := NewDerivedArtifactRepository(artifactTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	malformed := "zz" + string(make([]byte, 62))
	_, err := repo.Claim(ctx, claimInput(1, malformed, "owner", now))
	require.Error(t, err)
	require.Error(t, repo.Complete(ctx, completionInput(1, malformed, "owner", []byte("x"), now)))
	require.Error(t, repo.Fail(ctx, interfaces.ArtifactFailure{TenantID: 1, ArtifactKey: malformed, OwnerToken: "owner", FailedAt: now}))
	require.Error(t, repo.RenewLease(ctx, 1, malformed, "owner", now, time.Minute))
}

func TestDerivedArtifactConcurrentSQLiteClaimHasSingleOwner(t *testing.T) {
	repo := NewDerivedArtifactRepository(artifactTestDB(t))
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	key := artifactkey.DigestText("concurrent")
	const workers = 12
	var wg sync.WaitGroup
	outcomes := make(chan interfaces.ArtifactClaimOutcome, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := repo.Claim(ctx, claimInput(1, key, fmt.Sprintf("owner-%d", i), now))
			if err != nil {
				errs <- err
				return
			}
			outcomes <- result.Outcome
		}(i)
	}
	wg.Wait()
	close(errs)
	close(outcomes)
	for err := range errs {
		require.NoError(t, err)
	}
	claimed, busy := 0, 0
	for outcome := range outcomes {
		if outcome == interfaces.ArtifactClaimClaimed {
			claimed++
		} else if outcome == interfaces.ArtifactClaimBusy {
			busy++
		}
	}
	require.Equal(t, 1, claimed)
	require.Equal(t, workers-1, busy)
}

func TestDerivedArtifactModelUniqueConstraintAndNullableTimes(t *testing.T) {
	db := artifactTestDB(t)
	first := types.DerivedArtifact{TenantID: 1, ArtifactKey: "same", ArtifactKind: "k", InputDigest: "d", Status: types.DerivedArtifactPending}
	require.NoError(t, db.Create(&first).Error)
	duplicate := types.DerivedArtifact{TenantID: 1, ArtifactKey: "same", ArtifactKind: "k", InputDigest: "d", Status: types.DerivedArtifactPending}
	require.Error(t, db.Create(&duplicate).Error)
	other := types.DerivedArtifact{TenantID: 2, ArtifactKey: "same", ArtifactKind: "k", InputDigest: "d", Status: types.DerivedArtifactPending}
	require.NoError(t, db.Create(&other).Error)
	var loaded types.DerivedArtifact
	require.NoError(t, db.First(&loaded, first.ID).Error)
	require.Nil(t, loaded.LeaseExpiresAt)
	require.Nil(t, loaded.CompletedAt)
	require.True(t, db.Migrator().HasIndex(&types.DerivedArtifact{}, "idx_derived_artifacts_kind_status"))
	require.True(t, db.Migrator().HasIndex(&types.DerivedArtifact{}, "idx_derived_artifacts_lease"))
}
