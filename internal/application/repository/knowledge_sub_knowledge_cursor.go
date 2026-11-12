package repository

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalidSubKnowledgeCursor indicates an invalid or out-of-scope page cursor.
var ErrInvalidSubKnowledgeCursor = errors.New("invalid sub_knowledge cursor")

// The cursor is not an authorization token. Every request resolves the anchor
// KB grant again, and checks every cursor scope field against that anchor.
type subKnowledgeCursor struct {
	Tenant          uint64    `json:"tenant"`
	KB              string    `json:"kb"`
	SourceNamespace string    `json:"source_namespace"`
	RootID          string    `json:"root_id"`
	AsOf            time.Time `json:"as_of"`
	Rank            int       `json:"rank"`
	Order           int64     `json:"order"`
	SourceID        string    `json:"source_id"`
	KnowledgeID     string    `json:"knowledge_id"`
}

func parseSubKnowledgeCursor(raw string, tenant uint64, kb, sourceNamespace, group string) (subKnowledgeCursor, error) {
	c := subKnowledgeCursor{
		Tenant: tenant, KB: kb, SourceNamespace: sourceNamespace, RootID: group, AsOf: time.Now().UTC(),
	}
	if raw == "" {
		return c, nil
	}
	if len(raw) > 4096 {
		return c, ErrInvalidSubKnowledgeCursor
	}
	c = subKnowledgeCursor{}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.KB != kb ||
		c.SourceNamespace != sourceNamespace || c.RootID != group || c.AsOf.IsZero() ||
		c.AsOf.After(time.Now().Add(time.Second)) || c.Rank < 0 || c.Rank > 1 || c.Order < 0 ||
		c.SourceID == "" || len(c.SourceID) > 512 || c.KnowledgeID == "" || len(c.KnowledgeID) > 36 {
		return c, ErrInvalidSubKnowledgeCursor
	}
	return c, nil
}

func (c subKnowledgeCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
