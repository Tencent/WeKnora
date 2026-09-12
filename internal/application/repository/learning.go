package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"strings"
	"time"

	applogger "github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type learningRepository struct{ db *gorm.DB }

// NewLearningRepository creates a repository with content-free database diagnostics.
func NewLearningRepository(db *gorm.DB) interfaces.LearningRepository {
	// SQL diagnostics can contain serialized answer keys. Never inherit a
	// debug logger on this repository, including in test/runtime debug mode.
	return &learningRepository{
		db: db.Session(
			&gorm.Session{
				Logger:  logger.Default.LogMode(logger.Silent),
				NowFunc: func() time.Time { return time.Now().UTC() },
			},
		),
	}
}

func learningScope(db *gorm.DB, s interfaces.LearningScope) *gorm.DB {
	return db.Where("tenant_id = ? AND subject_id = ?", s.TenantID, s.SubjectID)
}

func validLearningScope(s interfaces.LearningScope) bool {
	return s.TenantID > 0 && strings.HasPrefix(s.SubjectID, types.PrincipalWebUser+":") &&
		len(s.SubjectID) > len(types.PrincipalWebUser)+1 && len(s.SubjectID) <= 128
}

func learningDBError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{
		types.ErrLearningForbidden, types.ErrLearningDisabled, types.ErrLearningNotFound,
		types.ErrLearningStale, types.ErrLearningNotReady, types.ErrLearningConflict, types.ErrLearningInvalid,
		types.ErrLearningBusy, types.ErrLearningEvidence, types.ErrLearningUnavailable,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.ErrLearningNotFound
	}
	if learningRetryable(err) {
		return types.ErrLearningBusy
	}

	// Driver messages can embed SQL, credentials or answers. Keep only bounded
	// diagnostic codes; never log or return the original error chain.
	fields := applogger.Fields{"component": "learning_repository", "error_class": "database"}
	var pg interface{ SQLState() string }
	var sq sqlite3.Error
	var network net.Error
	switch {
	case errors.As(err, &pg):
		fields["error_class"] = "postgres"
		code := pg.SQLState()
		if len(code) == 5 && strings.Trim(code, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			fields["sqlstate"] = code
		}
	case errors.As(err, &sq):
		fields["error_class"], fields["sqlite_code"] = "sqlite", int(sq.Code)
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, sql.ErrConnDone):
		fields["error_class"] = "connection"
	case errors.As(err, &network):
		fields["error_class"] = "network"
	}
	applogger.ErrorWithFields(ctx, nil, fields)
	return types.ErrLearningUnavailable
}

func learningRetryable(err error) bool {
	var sq sqlite3.Error
	if errors.As(err, &sq) {
		return sq.Code == sqlite3.ErrBusy || sq.Code == sqlite3.ErrLocked
	}
	var pg interface{ SQLState() string }
	return errors.As(err, &pg) && (pg.SQLState() == "40001" || pg.SQLState() == "40P01")
}

func (r *learningRepository) transaction(ctx context.Context, f func(*gorm.DB) error) error {
	for i := 0; ; i++ {
		err := r.db.WithContext(ctx).Transaction(f)
		if !learningRetryable(err) || i == 7 {
			return learningDBError(ctx, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 10 * time.Millisecond):
		}
	}
}

func (r *learningRepository) profileTx(ctx context.Context, scope interfaces.LearningScope, create, enabled bool,
	f func(*gorm.DB, *types.LearningProfile) error,
) error {
	if !validLearningScope(scope) {
		return types.ErrLearningForbidden
	}
	return r.transaction(ctx, func(tx *gorm.DB) error {
		if create {
			p := types.LearningProfile{TenantID: scope.TenantID, SubjectID: scope.SubjectID, Epoch: 1}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p).Error; err != nil {
				return err
			}
		}
		// First take a write lock, THEN read. SQLite's deferred transactions
		// otherwise race while upgrading a read snapshot to a write lock.
		res := learningScope(tx, scope).Model(&types.LearningProfile{}).UpdateColumn("epoch", gorm.Expr("epoch"))
		if res.Error != nil {
			return res.Error
		}
		p := &types.LearningProfile{TenantID: scope.TenantID, SubjectID: scope.SubjectID}
		if res.RowsAffected != 0 {
			if err := learningScope(tx, scope).First(p).Error; err != nil {
				return err
			}
		}
		if enabled && !p.Enabled {
			return types.ErrLearningDisabled
		}
		return f(tx, p)
	})
}

func learningShare(db *gorm.DB) *gorm.DB {
	if db.Name() == "postgres" {
		return db.Clauses(clause.Locking{Strength: "SHARE"})
	}
	return db
}

func (r *learningRepository) Settings(
	ctx context.Context,
	scope interfaces.LearningScope,
) (*types.LearningSettings, error) {
	if !validLearningScope(scope) {
		return nil, types.ErrLearningForbidden
	}
	var p types.LearningProfile
	err := learningScope(r.db.WithContext(ctx), scope).First(&p).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, learningDBError(ctx, err)
	}
	return &types.LearningSettings{Enabled: p.Enabled, AlgorithmVersion: types.LearningAlgorithmVersion}, nil
}

func (r *learningRepository) SetEnabled(
	ctx context.Context,
	scope interfaces.LearningScope,
	enabled bool,
) (*types.LearningSettings, error) {
	err := r.profileTx(ctx, scope, true, false, func(tx *gorm.DB, p *types.LearningProfile) error {
		if p.Enabled == enabled {
			return nil
		}
		if err := learningScope(tx, scope).Model(p).
			Updates(map[string]any{"enabled": enabled, "epoch": p.Epoch + 1}).
			Error; err != nil {
			return err
		}
		return learningScope(tx, scope).Model(&types.LearningQuiz{}).
			Where("status IN ?", []string{"pending", "running", "ready"}).
			Updates(map[string]any{"status": "stale", "lease_token": "", "lease_until": nil}).
			Error
	})
	if err != nil {
		return nil, err
	}
	return &types.LearningSettings{Enabled: enabled, AlgorithmVersion: types.LearningAlgorithmVersion}, nil
}

func learningKB(tx *gorm.DB, tenant uint64, id string) (*types.KnowledgeBase, error) {
	var kb types.KnowledgeBase
	if err := learningShare(tx).Where("id = ? AND tenant_id = ?", id, tenant).First(&kb).Error; err != nil {
		return nil, err
	}
	if !kb.IsWikiEnabled() || kb.IsTemporary {
		return nil, types.ErrLearningNotFound
	}
	return &kb, nil
}

func learningPage(tx *gorm.DB, tenant uint64, id string) (*types.WikiPage, *types.KnowledgeBase, error) {
	var initial types.WikiPage
	if err := tx.Select("id", "knowledge_base_id").
		Where("id = ? AND tenant_id = ?", id, tenant).
		First(&initial).
		Error; err != nil {
		return nil, nil, err
	}
	kb, err := learningKB(tx, tenant, initial.KnowledgeBaseID)
	if err != nil {
		return nil, nil, err
	}
	var p types.WikiPage
	err = learningShare(tx).Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", id, tenant, kb.ID).
		First(&p).
		Error
	if err != nil {
		return nil, nil, err
	}
	if !types.LearningEligiblePage(&p) {
		return nil, nil, types.ErrLearningNotFound
	}
	return &p, kb, nil
}

func learningPages(tx *gorm.DB, tenant uint64, kb string) *gorm.DB {
	return tx.Model(&types.WikiPage{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND status = ? AND page_type IN ?",
			tenant, kb, types.WikiPageStatusPublished, []string{"entity", "concept", "synthesis", "comparison"})
}

func learningMastery(tx *gorm.DB, scope interfaces.LearningScope, p *types.WikiPage) (*types.LearningMastery, error) {
	var m types.LearningMastery
	err := learningScope(tx, scope).Where("page_id = ?", p.ID).First(&m).Error
	if err == nil {
		return &m, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &types.LearningMastery{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, PageID: p.ID,
		KnowledgeBaseID: p.KnowledgeBaseID, PMastery: types.LearningInitialMastery,
	}, nil
}

func learningSaveMastery(tx *gorm.DB, m *types.LearningMastery) error {
	scope := interfaces.LearningScope{TenantID: m.TenantID, SubjectID: m.SubjectID}
	var n int64
	if err := learningScope(tx, scope).Model(&types.LearningMastery{}).
		Where("page_id <> ?", m.PageID).
		Count(&n).
		Error; err != nil {
		return err
	}
	if n >= types.LearningMaxNodes {
		return types.ErrLearningBusy
	}
	return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(m).Error
}
