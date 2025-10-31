package postgres

import (
	"encoding/json"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

func toTagCategoryModel(src *types.TagCategory) tagCategoryModel {
	return tagCategoryModel{
		ID:          src.ID,
		Name:        src.Name,
		Description: src.Description,
		Color:       src.Color,
		CreatedBy:   nullableInt64(src.CreatedBy),
		CreatedAt:   src.CreatedAt,
		UpdatedAt:   src.UpdatedAt,
	}
}

func fromTagCategoryModel(model tagCategoryModel, dest *types.TagCategory) {
	dest.ID = model.ID
	dest.Name = model.Name
	dest.Description = model.Description
	dest.Color = model.Color
	dest.CreatedAt = model.CreatedAt
	dest.UpdatedAt = model.UpdatedAt
	if model.CreatedBy != nil {
		dest.CreatedBy = *model.CreatedBy
	}
}

func toTagModel(src *types.Tag) tagModel {
	return tagModel{
		ID:          src.ID,
		CategoryID:  src.CategoryID,
		Name:        src.Name,
		Value:       src.Value,
		Weight:      src.Weight,
		Description: src.Description,
		CreatedBy:   nullableInt64(src.CreatedBy),
		CreatedAt:   src.CreatedAt,
		UpdatedAt:   src.UpdatedAt,
	}
}

func fromTagModel(model tagModel, dest *types.Tag) {
	dest.ID = model.ID
	dest.CategoryID = model.CategoryID
	dest.Name = model.Name
	dest.Value = model.Value
	dest.Weight = model.Weight
	dest.Description = model.Description
	dest.CreatedAt = model.CreatedAt
	dest.UpdatedAt = model.UpdatedAt
	if model.CreatedBy != nil {
		dest.CreatedBy = *model.CreatedBy
	}
}

func toDocumentTagModel(src *types.DocumentTag) documentTagModel {
	return documentTagModel{
		ID:         src.ID,
		DocumentID: src.DocumentID,
		TagID:      src.TagID,
		Weight:     src.Weight,
		CreatedBy:  nullableInt64(src.CreatedBy),
		CreatedAt:  src.CreatedAt,
		UpdatedAt:  src.UpdatedAt,
	}
}

func fromDocumentTagModel(model documentTagModel, dest *types.DocumentTag) {
	dest.ID = model.ID
	dest.DocumentID = model.DocumentID
	dest.TagID = model.TagID
	dest.Weight = model.Weight
	dest.CreatedAt = model.CreatedAt
	dest.UpdatedAt = model.UpdatedAt
	if model.CreatedBy != nil {
		dest.CreatedBy = *model.CreatedBy
	}
}

func toVirtualKBModel(src *types.VirtualKB) (virtualKBModel, error) {
	var config any
	if src.Config != nil {
		bytes, err := json.Marshal(src.Config)
		if err != nil {
			return virtualKBModel{}, err
		}
		config = json.RawMessage(bytes)
	}

	filters := make([]virtualKBFilterModel, 0, len(src.Filters))
	for _, filter := range src.Filters {
		filters = append(filters, virtualKBFilterModel{
			ID:            filter.ID,
			VirtualKBID:   filter.VirtualKBID,
			TagCategoryID: filter.TagCategoryID,
			Operator:      filter.Operator,
			Weight:        filter.Weight,
			TagIDs:        filter.TagIDs,
		})
	}

	return virtualKBModel{
		ID:          src.ID,
		Name:        src.Name,
		Description: src.Description,
		CreatedBy:   nullableInt64(src.CreatedBy),
		Config:      config,
		CreatedAt:   src.CreatedAt,
		UpdatedAt:   src.UpdatedAt,
		Filters:     filters,
	}, nil
}

func fromVirtualKBModel(model virtualKBModel, dest *types.VirtualKB) error {
	dest.ID = model.ID
	dest.Name = model.Name
	dest.Description = model.Description
	dest.CreatedAt = model.CreatedAt
	dest.UpdatedAt = model.UpdatedAt
	if model.CreatedBy != nil {
		dest.CreatedBy = *model.CreatedBy
	}

	if model.Config != nil {
		bytes, err := json.Marshal(model.Config)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(bytes, &dest.Config); err != nil {
			return err
		}
	}

	dest.Filters = make([]types.VirtualKBFilter, 0, len(model.Filters))
	for _, filter := range model.Filters {
		dest.Filters = append(dest.Filters, types.VirtualKBFilter{
			ID:            filter.ID,
			VirtualKBID:   filter.VirtualKBID,
			TagCategoryID: filter.TagCategoryID,
			Operator:      filter.Operator,
			Weight:        filter.Weight,
			TagIDs:        filter.TagIDs,
		})
	}

	return nil
}

func nullableInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
