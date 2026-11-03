package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
)

// This fixture exercises manual reparse enqueue errors/override propagation;
// real ownership CAS and busy rejection are tested against the repository.
func (r *reparseFailureKnowledgeRepo) ClaimKnowledgeForReparse(
	_ context.Context,
	before *types.Knowledge,
	_ bool,
) (*types.Knowledge, error) {
	fields, _ := before.Metadata.Map()
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields[types.KnowledgeReparseMetadataKey] = "manual-fixture-claim"
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	before.Metadata = types.JSON(encoded)
	before.ParseStatus = types.ParseStatusProcessing
	r.knowledge = before
	return before, nil
}

func (r *reparseFailureKnowledgeRepo) FailKnowledgeReparseClaim(
	_ context.Context,
	claim *types.Knowledge,
	message string,
) (*types.Knowledge, error) {
	claim.ParseStatus = types.ParseStatusFailed
	claim.ErrorMessage = message
	r.knowledge = claim
	return claim, nil
}
