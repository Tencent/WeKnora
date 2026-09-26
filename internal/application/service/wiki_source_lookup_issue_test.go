package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiIssueLookupFailure struct {
	interfaces.KnowledgeService
	err error
}

func (s *wikiIssueLookupFailure) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return nil, s.err
}

func TestWikiSourceLookupFailureRepro(t *testing.T) {
	for _, lookupErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database unavailable")} {
		t.Run(lookupErr.Error(), func(t *testing.T) {
			svc := &wikiIngestService{knowledgeSvc: &wikiIssueLookupFailure{err: lookupErr}}
			_, _, err := svc.mapOneDocument(context.Background(), nil,
				WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
				WikiPendingOp{KnowledgeID: "k-1", Op: WikiOpIngest}, nil)
			if !errors.Is(err, lookupErr) {
				t.Errorf("Map must preserve lookup failure: got %v, want %v", err, lookupErr)
			}
			_, _, _, err = svc.reduceSlugUpdates(context.Background(), nil, "kb-1", "entity/example",
				[]SlugUpdate{{Slug: "entity/example", Type: "entity", KnowledgeID: "k-1"}}, 7, nil, nil)
			if !errors.Is(err, lookupErr) {
				t.Errorf("Reduce must preserve lookup failure: got %v, want %v", err, lookupErr)
			}
		})
	}
}
