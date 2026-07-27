package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type agentCollectionFilterRequest struct {
	TenantID    uint64 `json:"tenant_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Keyword     string `json:"keyword,omitempty"`
	Complete    *bool  `json:"complete,omitempty"`
	UpdatedFrom string `json:"updated_from,omitempty"`
	UpdatedTo   string `json:"updated_to,omitempty"`
	FieldKey    string `json:"field_key,omitempty"`
	FieldValue  string `json:"field_value,omitempty"`
	Page        int    `json:"page,omitempty"`
	PageSize    int    `json:"page_size,omitempty"`
}

func agentCollectionFilterFromQuery(c *gin.Context) (types.AgentCollectionProfileFilter, error) {
	request := agentCollectionFilterRequest{
		AgentID: strings.TrimSpace(c.Query("agent_id")), UserID: strings.TrimSpace(c.Query("user_id")),
		Keyword: strings.TrimSpace(c.Query("keyword")), UpdatedFrom: strings.TrimSpace(c.Query("updated_from")),
		UpdatedTo: strings.TrimSpace(c.Query("updated_to")), FieldKey: strings.TrimSpace(c.Query("field_key")),
		FieldValue: strings.TrimSpace(c.Query("field_value")),
	}
	var err error
	if raw := strings.TrimSpace(c.Query("tenant_id")); raw != "" {
		request.TenantID, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid tenant_id")
		}
	}
	if raw := strings.TrimSpace(c.Query("complete")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid complete")
		}
		request.Complete = &value
	}
	request.Page, err = optionalPositiveInt(c.Query("page"), 1)
	if err != nil {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid page")
	}
	request.PageSize, err = optionalPositiveInt(c.Query("page_size"), 20)
	if err != nil {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid page_size")
	}
	return request.collectionFilter()
}

func (r agentCollectionFilterRequest) collectionFilter() (types.AgentCollectionProfileFilter, error) {
	if r.FieldValue != "" && strings.TrimSpace(r.FieldKey) == "" {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("field_key is required with field_value")
	}
	from, err := optionalCollectionTime(r.UpdatedFrom)
	if err != nil {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid updated_from")
	}
	to, err := optionalCollectionTime(r.UpdatedTo)
	if err != nil {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("invalid updated_to")
	}
	if from != nil && to != nil && from.After(*to) {
		return types.AgentCollectionProfileFilter{}, fmt.Errorf("updated_from must not exceed updated_to")
	}
	return types.AgentCollectionProfileFilter{
		TenantID: r.TenantID, AgentID: strings.TrimSpace(r.AgentID), UserID: strings.TrimSpace(r.UserID),
		Keyword: strings.TrimSpace(r.Keyword), Complete: r.Complete, UpdatedFrom: from, UpdatedTo: to,
		FieldKey: strings.TrimSpace(r.FieldKey), FieldValue: strings.TrimSpace(r.FieldValue),
		Page: r.Page, PageSize: r.PageSize,
	}, nil
}

func optionalPositiveInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("value must be positive")
	}
	return value, nil
}

func optionalCollectionTime(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
