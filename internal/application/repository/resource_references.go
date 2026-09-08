package repository

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func (r *resourceRepository) IsReferencedByKnowledgeBase(
	ctx context.Context,
	tenantID uint64,
	kbID, resourceID string,
	references []string,
) (bool, error) {
	if tenantID == 0 || kbID == "" {
		return false, nil
	}
	if resourceID != "" {
		var count int64
		err := r.db.WithContext(ctx).Table("resource_bindings AS b").
			Joins("JOIN knowledges AS k ON k.id = b.owner_id AND k.tenant_id = b.tenant_id").
			Where("b.resource_id = ? AND b.owner_type = ? AND b.tenant_id = ? AND "+
				"k.knowledge_base_id = ? AND k.deleted_at IS NULL",
				resourceID,
				types.ResourceOwnerKnowledge,
				tenantID,
				kbID).
			Count(&count).Error
		if err != nil || count > 0 {
			return count > 0, err
		}
	}
	// Only project candidate text fields and stream rows. SQL LIKE narrows the
	// scan; exact token matching below rejects prefixes and wildcard lookalikes.
	for _, source := range []struct {
		table   string
		columns []string
	}{
		{"chunks", []string{"content", "image_info"}},
		{"wiki_pages", []string{"content"}},
	} {
		var clauses []string
		var args []interface{}
		for _, ref := range references {
			if ref == "" {
				continue
			}
			pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(ref) + "%"
			for _, column := range source.columns {
				clauses = append(clauses, column+" LIKE ? ESCAPE '!'")
				args = append(args, pattern)
			}
		}
		if len(clauses) == 0 {
			return false, nil
		}
		rows, err := r.db.WithContext(ctx).Table(source.table).Select(strings.Join(source.columns, ", ")).
			Where("tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", tenantID, kbID).
			Where("("+strings.Join(clauses, " OR ")+")", args...).Rows()
		if err != nil {
			return false, err
		}
		found := false
		for rows.Next() {
			values := make([]*string, len(source.columns))
			dest := make([]interface{}, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				break
			}
			for _, value := range values {
				if value != nil {
					for _, ref := range references {
						if types.ContainsStorageReference(*value, ref) {
							found = true
						}
					}
				}
			}
			if found {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}
