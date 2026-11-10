package access

import (
	"encoding/json"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// RejectMovingKnowledge is shared by admission and persisted service checks.
// Admission prevents misleading task acceptance; workers must recheck after
// enqueue because a move can claim the document in the intervening window.
func RejectMovingKnowledge(knowledge *types.Knowledge) error {
	if knowledge == nil {
		return apperrors.NewNotFoundError("knowledge not found")
	}
	fields := map[string]json.RawMessage{}
	if len(knowledge.Metadata) != 0 {
		if err := json.Unmarshal(knowledge.Metadata, &fields); err != nil {
			return err
		}
	}
	raw := fields[types.KnowledgeTransferMetadataKey]
	if len(raw) == 0 {
		return nil
	}
	var state struct {
		Operation KBTransferOperation `json:"operation"`
		Phase     string              `json:"phase"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Operation == KBTransferMove && state.Phase == "moving" {
		return apperrors.NewConflictError("knowledge has an unfinished move; retry the move first")
	}
	return nil
}
