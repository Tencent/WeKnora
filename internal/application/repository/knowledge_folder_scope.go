package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type knowledgeFolderScopeRepository struct {
	db              *gorm.DB
	sqliteRetryWait knowledgeFolderSQLiteWaitFunc
}

type knowledgeFolderScopeReader struct {
	db              *gorm.DB
	ctx             context.Context
	sourceTenantID  uint64
	knowledgeBaseID string
}

const knowledgeFolderScopeReadBatchSize = knowledgeFolderNamesBatchSize

var _ interfaces.KnowledgeFolderScopeRepository = (*knowledgeFolderScopeRepository)(nil)
var _ interfaces.KnowledgeFolderScopeReader = (*knowledgeFolderScopeReader)(nil)

// NewKnowledgeFolderScopeRepository creates the narrow folder scope repository.
func NewKnowledgeFolderScopeRepository(db *gorm.DB) interfaces.KnowledgeFolderScopeRepository {
	return &knowledgeFolderScopeRepository{db: db}
}

func (r *knowledgeFolderScopeRepository) RunKnowledgeFolderScopeReadSnapshot(
	ctx context.Context,
	sourceTenantID uint64,
	knowledgeBaseID string,
	fn interfaces.KnowledgeFolderScopeReadSnapshotFunc,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if sourceTenantID == 0 || knowledgeBaseID == "" ||
		knowledgeBaseID != strings.TrimSpace(knowledgeBaseID) {
		return fmt.Errorf("%w: knowledge folder scope is empty", ErrKnowledgeFolderInvalid)
	}
	if fn == nil {
		return fmt.Errorf("%w: read snapshot callback is nil", ErrKnowledgeFolderInvalid)
	}
	if r == nil || r.db == nil || r.db.Config == nil ||
		r.db.Dialector == nil {
		return ErrKnowledgeFolderUnsupportedDialect
	}

	runSnapshot := func(options *sql.TxOptions) error {
		err := r.db.WithContext(ctx).Transaction(
			func(tx *gorm.DB) error {
				return fn(&knowledgeFolderScopeReader{
					db:              tx,
					ctx:             ctx,
					sourceTenantID:  sourceTenantID,
					knowledgeBaseID: knowledgeBaseID,
				})
			},
			options,
		)
		return preserveKnowledgeFolderScopeContextError(ctx, err)
	}

	switch r.db.Dialector.Name() {
	case "postgres":
		return runSnapshot(&sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		})
	case "sqlite":
		waitFn := r.sqliteRetryWait
		if waitFn == nil {
			waitFn = waitKnowledgeFolderSQLiteRetry
		}
		return runKnowledgeFolderSQLiteRetry(
			ctx,
			func() error {
				return runSnapshot(&sql.TxOptions{ReadOnly: true})
			},
			waitFn,
		)
	default:
		return fmt.Errorf(
			"%w: %s",
			ErrKnowledgeFolderUnsupportedDialect,
			r.db.Dialector.Name(),
		)
	}
}

func (r *knowledgeFolderScopeReader) ListScopeFoldersByIDs(
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderScopeRead(r); err != nil {
		return nil, err
	}
	orderedIDs, err := normalizeKnowledgeFolderScopeIDs(folderIDs)
	if err != nil {
		return nil, err
	}
	if len(orderedIDs) == 0 {
		return []*types.KnowledgeFolder{}, nil
	}

	folders := make([]*types.KnowledgeFolder, 0, len(orderedIDs))
	for start := 0; start < len(orderedIDs); start += knowledgeFolderScopeReadBatchSize {
		end := min(start+knowledgeFolderScopeReadBatchSize, len(orderedIDs))
		var batch []*types.KnowledgeFolder
		err = r.db.WithContext(r.ctx).
			Where(
				`tenant_id = ? AND knowledge_base_id = ?
					AND id IN ? AND deleted_at IS NULL`,
				r.sourceTenantID,
				r.knowledgeBaseID,
				orderedIDs[start:end],
			).
			Order("depth ASC").
			Order("path ASC").
			Order("id ASC").
			Find(&batch).Error
		if err != nil {
			return nil, fmt.Errorf("list knowledge folder scope rows: %w", err)
		}
		folders = append(folders, batch...)
	}
	sortKnowledgeFolderScopeRows(folders)
	return folders, nil
}

func (r *knowledgeFolderScopeReader) ListScopeSubtreeCandidates(
	roots []interfaces.KnowledgeFolderScopeRoot,
	limit int,
) ([]*types.KnowledgeFolder, error) {
	if err := validateKnowledgeFolderScopeRead(r); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: subtree candidate limit must be positive", ErrKnowledgeFolderInvalid)
	}
	orderedRoots, err := normalizeKnowledgeFolderScopeRoots(roots)
	if err != nil {
		return nil, err
	}
	if len(orderedRoots) == 0 {
		return []*types.KnowledgeFolder{}, nil
	}

	rootValues := make([]string, len(orderedRoots))
	pathClauses := make([]string, len(orderedRoots))
	queryArgs := make([]interface{}, 0, 3*len(orderedRoots)+7)
	for index, root := range orderedRoots {
		rootValues[index] = "(CAST(? AS VARCHAR(36)), CAST(? AS VARCHAR(2048)))"
		pathClauses[index] = knowledgeFolderPathLikeClause
		queryArgs = append(queryArgs, root.ID, root.Path)
	}
	queryArgs = append(
		queryArgs,
		r.sourceTenantID,
		r.knowledgeBaseID,
		r.sourceTenantID,
		r.knowledgeBaseID,
	)
	for _, root := range orderedRoots {
		queryArgs = append(
			queryArgs,
			knowledgeFolderPathPrefixPattern(root.Path),
		)
	}
	queryArgs = append(
		queryArgs,
		r.sourceTenantID,
		r.knowledgeBaseID,
		limit,
	)

	query := fmt.Sprintf(`
		WITH RECURSIVE selected_roots(id, path) AS (
			VALUES %s
		),
		reachable(id) AS (
			SELECT id FROM selected_roots
			UNION
			SELECT child.id
			FROM knowledge_folders AS child
			INNER JOIN reachable AS parent ON child.parent_id = parent.id
			WHERE child.tenant_id = ?
				AND child.knowledge_base_id = ?
				AND child.deleted_at IS NULL
		),
		candidates(id) AS (
			SELECT id FROM reachable
			UNION
			SELECT id
			FROM knowledge_folders
			WHERE tenant_id = ?
				AND knowledge_base_id = ?
				AND deleted_at IS NULL
				AND (%s)
		)
		SELECT folder.*
		FROM knowledge_folders AS folder
		INNER JOIN candidates ON candidates.id = folder.id
		WHERE folder.tenant_id = ?
			AND folder.knowledge_base_id = ?
			AND folder.deleted_at IS NULL
		ORDER BY folder.depth ASC, folder.path ASC, folder.id ASC
		LIMIT ?
		`, strings.Join(rootValues, ", "), strings.Join(pathClauses, " OR "))
	var folders []*types.KnowledgeFolder
	if err := r.db.WithContext(r.ctx).Raw(query, queryArgs...).
		Scan(&folders).Error; err != nil {
		return nil, fmt.Errorf("list knowledge folder subtree candidates: %w", err)
	}
	sortKnowledgeFolderScopeRows(folders)
	return folders, nil
}

func validateKnowledgeFolderScopeRead(
	reader *knowledgeFolderScopeReader,
) error {
	if reader == nil || reader.db == nil || reader.db.Config == nil ||
		reader.db.Dialector == nil {
		return fmt.Errorf(
			"%w: knowledge folder scope reader is invalid",
			ErrKnowledgeFolderInvalid,
		)
	}
	if reader.ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrKnowledgeFolderInvalid)
	}
	if err := reader.ctx.Err(); err != nil {
		return err
	}
	if reader.sourceTenantID == 0 || reader.knowledgeBaseID == "" ||
		reader.knowledgeBaseID != strings.TrimSpace(reader.knowledgeBaseID) {
		return fmt.Errorf("%w: knowledge folder scope is empty", ErrKnowledgeFolderInvalid)
	}
	return nil
}

func normalizeKnowledgeFolderScopeIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{}, nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, fmt.Errorf("%w: invalid knowledge folder scope id", ErrKnowledgeFolderInvalid)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeKnowledgeFolderScopeRoots(
	values []interfaces.KnowledgeFolderScopeRoot,
) ([]interfaces.KnowledgeFolderScopeRoot, error) {
	if len(values) == 0 {
		return []interfaces.KnowledgeFolderScopeRoot{}, nil
	}
	pathsByID := make(map[string]string, len(values))
	for _, root := range values {
		if root.ID == "" || root.ID != strings.TrimSpace(root.ID) {
			return nil, fmt.Errorf("%w: invalid knowledge folder scope root id", ErrKnowledgeFolderInvalid)
		}
		if err := validateKnowledgeFolderScopeRootPath(root); err != nil {
			return nil, err
		}
		if path, exists := pathsByID[root.ID]; exists {
			if path != root.Path {
				return nil, fmt.Errorf(
					"%w: duplicate knowledge folder scope root has different path",
					ErrKnowledgeFolderInvalid,
				)
			}
			continue
		}
		pathsByID[root.ID] = root.Path
	}

	normalized := make([]interfaces.KnowledgeFolderScopeRoot, 0, len(pathsByID))
	for id, path := range pathsByID {
		normalized = append(normalized, interfaces.KnowledgeFolderScopeRoot{
			ID:   id,
			Path: path,
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].ID != normalized[j].ID {
			return normalized[i].ID < normalized[j].ID
		}
		return normalized[i].Path < normalized[j].Path
	})
	return normalized, nil
}

func validateKnowledgeFolderScopeRootPath(
	root interfaces.KnowledgeFolderScopeRoot,
) error {
	if root.Path == "" ||
		root.Path != strings.TrimSpace(root.Path) ||
		!strings.HasPrefix(root.Path, "/") ||
		!strings.HasSuffix(root.Path, "/") {
		return fmt.Errorf("%w: invalid knowledge folder scope root path", ErrKnowledgeFolderInvalid)
	}
	innerPath := strings.TrimSuffix(strings.TrimPrefix(root.Path, "/"), "/")
	if innerPath == "" {
		return fmt.Errorf("%w: invalid knowledge folder scope root path", ErrKnowledgeFolderInvalid)
	}
	pathIDs := strings.Split(innerPath, "/")
	if len(pathIDs) > types.KnowledgeFolderMaxDepth {
		return fmt.Errorf("%w: invalid knowledge folder scope root path", ErrKnowledgeFolderInvalid)
	}
	for _, pathID := range pathIDs {
		if pathID == "" {
			return fmt.Errorf("%w: invalid knowledge folder scope root path", ErrKnowledgeFolderInvalid)
		}
	}
	if pathIDs[len(pathIDs)-1] != root.ID {
		return fmt.Errorf("%w: invalid knowledge folder scope root path", ErrKnowledgeFolderInvalid)
	}
	return nil
}

func sortKnowledgeFolderScopeRows(folders []*types.KnowledgeFolder) {
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Depth != folders[j].Depth {
			return folders[i].Depth < folders[j].Depth
		}
		if folders[i].Path != folders[j].Path {
			return folders[i].Path < folders[j].Path
		}
		return folders[i].ID < folders[j].ID
	})
}

func preserveKnowledgeFolderScopeContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}
