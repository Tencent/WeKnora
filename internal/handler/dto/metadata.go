package dto

import (
	"bytes"
	"encoding/json"
	"fmt"
)

import "github.com/Tencent/WeKnora/internal/types"

type ConfigureMetadataOptionRequest struct {
	ID        string               `json:"id"`
	Label     string               `json:"label" binding:"required"`
	Status    types.MetadataStatus `json:"status"`
	SortOrder int                  `json:"sort_order"`
}

type ConfigureMetadataDefinitionRequest struct {
	Name        string                           `json:"name" binding:"required"`
	Description string                           `json:"desc"`
	ValueType   types.MetadataValueType          `json:"value_type" binding:"required"`
	Required    bool                             `json:"required"`
	Filterable  bool                             `json:"filterable"`
	SortOrder   int                              `json:"sort_order"`
	Options     []ConfigureMetadataOptionRequest `json:"options"`
}

func (r ConfigureMetadataDefinitionRequest) Command(
	knowledgeBaseID string,
	definitionID string,
) types.ConfigureMetadataDefinition {
	options := make([]types.ConfigureMetadataOption, 0, len(r.Options))
	for _, option := range r.Options {
		options = append(options, types.ConfigureMetadataOption{
			ID:        option.ID,
			Label:     option.Label,
			Status:    option.Status,
			SortOrder: option.SortOrder,
		})
	}
	return types.ConfigureMetadataDefinition{
		KnowledgeBaseID: knowledgeBaseID,
		DefinitionID:    definitionID,
		Name:            r.Name,
		Description:     r.Description,
		ValueType:       r.ValueType,
		Required:        r.Required,
		Filterable:      r.Filterable,
		SortOrder:       r.SortOrder,
		Options:         options,
	}
}

type ConfigureMetadataAutoRuleRequest struct {
	Strategy types.MetadataRuleStrategy `json:"strategy" binding:"required"`
	Config   types.JSONMap              `json:"config" binding:"required"`
}

type BatchReadDocumentMetadataRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1,max=200,dive,required"`
}

type ChangeDocumentMetadataRequest struct {
	Changes []MetadataValueChangeRequest `json:"changes" binding:"required,min=1,dive"`
}

type MetadataValueChangeRequest struct {
	MetadataDefinitionID string          `json:"metadata_definition_id" binding:"required"`
	Value                json.RawMessage `json:"value"`
	AllowAutoOverwrite   *bool           `json:"allow_auto_overwrite"`
	ExpectedVersion      *int            `json:"expected_version"`
}

func (r MetadataValueChangeRequest) Change() (types.MetadataValueChange, error) {
	change := types.MetadataValueChange{
		MetadataDefinitionID: r.MetadataDefinitionID,
		AllowAutoOverwrite:   r.AllowAutoOverwrite,
		ExpectedVersion:      r.ExpectedVersion,
	}
	if r.Value == nil {
		return change, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(r.Value))
	decoder.UseNumber()
	if err := decoder.Decode(&change.Value); err != nil {
		return types.MetadataValueChange{}, fmt.Errorf("decode metadata value: %w", err)
	}
	change.ValueSet = true
	return change, nil
}

type ConfirmDocumentMetadataRequest struct {
	MetadataDefinitionIDs []string `json:"metadata_definition_ids"`
}

type RerunMetadataAutoFillRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1,max=500,dive,required"`
}
