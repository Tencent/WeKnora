package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// Flipping the image vector switch invalidates every stored vector: the
// vectors do not move, the queries do. Without a re-index following the flip,
// retrieval keeps returning k results that are quietly the wrong ones.

type reindexTriggerEnqueuer struct {
	tasks []*asynq.Task
}

func (e *reindexTriggerEnqueuer) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{ID: "task-1"}, nil
}

func reindexTriggerKB(imageVector bool) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               "kb-trigger",
		TenantID:         3,
		EmbeddingModelID: "model-1",
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled:      true,
			ImageVectorEnabled: imageVector,
		},
	}
}

func newTriggerService(enqueuer interfaces.TaskEnqueuer) *knowledgeBaseService {
	return &knowledgeBaseService{
		modelService: &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: true}},
		asynqClient:  enqueuer,
	}
}

// Both directions matter: either way every stored vector is stranded.
func TestEnqueueVectorReindexOnEnvelopeFlip(t *testing.T) {
	for _, tc := range []struct {
		name string
		from bool
		to   bool
	}{
		{"switch turned on", false, true},
		{"switch turned off", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enqueuer := &reindexTriggerEnqueuer{}
			svc := newTriggerService(enqueuer)

			svc.enqueueVectorReindexIfEnvelopeChanged(context.Background(),
				reindexTriggerKB(tc.to), tc.from)

			require.Len(t, enqueuer.tasks, 1, "the envelope moved, so the stored vectors are stranded")
			require.Equal(t, types.TypeKBReindexVectors, enqueuer.tasks[0].Type())
		})
	}
}

// Renaming a knowledge base must not trigger a KB-wide re-embed.
func TestEnqueueVectorReindexIgnoresIrrelevantChanges(t *testing.T) {
	enqueuer := &reindexTriggerEnqueuer{}
	kb := reindexTriggerKB(true)
	svc := newTriggerService(enqueuer)

	svc.enqueueVectorReindexIfEnvelopeChanged(context.Background(), kb, true)

	require.Empty(t, enqueuer.tasks, "the envelope did not move, so nothing needs re-encoding")
}

// Why the trigger keys on the envelope rather than the switch: a text-only
// model cannot enter the chat-template space, so flipping the switch changes
// nothing about how queries are encoded.
func TestEnqueueVectorReindexIgnoresToggleOnATextOnlyModel(t *testing.T) {
	enqueuer := &reindexTriggerEnqueuer{}
	svc := &knowledgeBaseService{
		modelService: &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: false}},
		asynqClient:  enqueuer,
	}
	kb := reindexTriggerKB(true)

	svc.enqueueVectorReindexIfEnvelopeChanged(context.Background(), kb, false)

	require.Empty(t, enqueuer.tasks,
		"the switch flipped but the envelope did not: a text-only model has only one space")
}

// The async handler re-injects the tenant from the payload, so a missing
// TenantID fails at run time, not compile time.
func TestEnqueueVectorReindexPayloadCarriesTenant(t *testing.T) {
	enqueuer := &reindexTriggerEnqueuer{}
	kb := reindexTriggerKB(true)
	svc := newTriggerService(enqueuer)

	svc.enqueueVectorReindexIfEnvelopeChanged(context.Background(), kb, false)

	require.Len(t, enqueuer.tasks, 1)
	var payload types.KBReindexVectorsPayload
	require.NoError(t, json.Unmarshal(enqueuer.tasks[0].Payload(), &payload))
	require.Equal(t, uint64(3), payload.TenantID)
	require.Equal(t, "kb-trigger", payload.KnowledgeBaseID)
}

type triggerKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *triggerKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

func (r *triggerKBRepo) UpdateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) error {
	copied := *kb
	r.kb = &copied
	return nil
}

// The whole path an operator takes. The trigger is only useful if the update
// path passes the envelope as it was BEFORE the save — afterwards both readings
// agree and the change is invisible.
func TestUpdateKnowledgeBaseQueuesReindexWhenSwitchFlips(t *testing.T) {
	enqueuer := &reindexTriggerEnqueuer{}
	existing := reindexTriggerKB(false)
	svc := &knowledgeBaseService{
		repo:         &triggerKBRepo{kb: existing},
		modelService: &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: true}},
		asynqClient:  enqueuer,
	}

	updated, err := svc.UpdateKnowledgeBase(context.Background(), "kb-trigger", "kb", "",
		&types.KnowledgeBaseConfig{
			IndexingStrategy: &types.IndexingStrategy{
				VectorEnabled:      true,
				ImageVectorEnabled: true,
			},
		})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Len(t, enqueuer.tasks, 1,
		"the saved strategy is in a different space than the stored vectors")
	require.Equal(t, types.TypeKBReindexVectors, enqueuer.tasks[0].Type())
}

// Control: saving an unchanged strategy must not re-embed the knowledge base.
func TestUpdateKnowledgeBaseWithoutStrategyChangeStaysQuiet(t *testing.T) {
	enqueuer := &reindexTriggerEnqueuer{}
	existing := reindexTriggerKB(true)
	svc := &knowledgeBaseService{
		repo:         &triggerKBRepo{kb: existing},
		modelService: &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: true}},
		asynqClient:  enqueuer,
	}

	_, err := svc.UpdateKnowledgeBase(context.Background(), "kb-trigger", "renamed", "",
		&types.KnowledgeBaseConfig{
			IndexingStrategy: &types.IndexingStrategy{
				VectorEnabled:      true,
				ImageVectorEnabled: true,
			},
		})

	require.NoError(t, err)
	require.Empty(t, enqueuer.tasks, "renaming a knowledge base must not re-embed it")
}
