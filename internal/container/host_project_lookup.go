//go:build desktop

package container

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type hostProjectLookupImpl struct {
	db       *gorm.DB
	approved func() []string
}

func newHostProjectLookup(db *gorm.DB, approved func() []string) localsandbox.ProjectLookup {
	return &hostProjectLookupImpl{db: db, approved: approved}
}

func hostProjectLookup(db *gorm.DB, dirs session.HostProjectDirsLoader) localsandbox.ProjectLookup {
	return newHostProjectLookup(db, dirs)
}

func (l *hostProjectLookupImpl) ProjectDirForSession(ctx context.Context, sessionID string) (string, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if l == nil || l.db == nil || sessionID == "" {
		return "", false, nil
	}
	var sess types.Session
	err := l.db.WithContext(ctx).
		Select("host_workspace_dir").
		Where("id = ?", sessionID).
		First(&sess).Error
	if err != nil {
		return "", false, nil
	}
	raw := strings.TrimSpace(sess.HostWorkspaceDir)
	if raw == "" {
		return "", false, nil
	}
	dir, ok := session.MatchApprovedDir(raw, l.approvedList())
	if !ok {
		return "", false, localsandbox.ErrProjectDirRevoked
	}
	return dir, true, nil
}

func (l *hostProjectLookupImpl) approvedList() []string {
	if l == nil || l.approved == nil {
		return nil
	}
	return l.approved()
}
