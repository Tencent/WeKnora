package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	graphExtractArtifactKind          = "graph.chunk-extraction"
	graphExtractArtifactSchemaVersion = "graph-chunk-extraction/v1"
	graphExtractPromptVersion         = "graph-extractor-prompt/v1"
	graphExtractProducerVersion       = "graph-extractor/v1"
	graphExtractArtifactLease         = 5 * time.Minute
	graphExtractArtifactWait          = 6 * time.Minute
	graphExtractArtifactPoll          = 100 * time.Millisecond
	graphExtractArtifactCleanup       = 2 * time.Second
)

type graphExtractArtifactTiming struct{ Lease, Wait, Poll, Cleanup time.Duration }

func (s *ChunkExtractService) graphArtifactTiming() graphExtractArtifactTiming {
	t := s.graphCacheTiming
	if t.Lease <= 0 {
		t.Lease = graphExtractArtifactLease
	}
	if t.Wait <= 0 {
		t.Wait = graphExtractArtifactWait
	}
	if t.Poll <= 0 {
		t.Poll = graphExtractArtifactPoll
	}
	if t.Cleanup <= 0 {
		t.Cleanup = graphExtractArtifactCleanup
	}
	return t
}

type graphExtractArtifactPayload struct {
	SchemaVersion string           `json:"schema_version"`
	Graph         *types.GraphData `json:"graph"`
}

func graphExtractArtifactKey(tenantID uint64, inputDigest, modelID, modelRevision, configDigest string) string {
	return artifactkey.Generate(artifactkey.KeyInput{
		Kind: graphExtractArtifactKind, TenantScope: fmt.Sprintf("tenant:%d", tenantID),
		InputDigest: inputDigest, ModelID: modelID, ModelRevision: modelRevision,
		PromptVersion: graphExtractPromptVersion, ConfigDigest: configDigest,
		ProducerVersion: graphExtractProducerVersion,
	})
}

func (s *ChunkExtractService) extractGraphCached(ctx context.Context, tenantID uint64, model chat.Chat, template *types.PromptTemplateStructured, content string) (*types.GraphData, types.JSONMap, error) {
	if s.artifactRepo == nil || tenantID == 0 {
		return extractGraphWithObservation(ctx, model, template, content)
	}
	inputDigest := artifactkey.DigestText(content)
	modelConfig := map[string]any{"runtime_model_name": model.GetModelName()}
	modelRevision := model.GetModelName()
	if s.modelService != nil && model.GetModelID() != "" {
		if configured, getErr := s.modelService.GetModelByID(ctx, model.GetModelID()); getErr == nil && configured != nil {
			modelConfig = map[string]any{
				"name": configured.Name, "source": configured.Source,
				"provider":          configured.Parameters.Provider,
				"interface_type":    configured.Parameters.InterfaceType,
				"parameter_size":    configured.Parameters.ParameterSize,
				"remote_model_name": configured.Parameters.ExtraConfig["remote_model_name"],
				"model_revision":    configured.Parameters.ExtraConfig["model_revision"],
				"revision":          configured.Parameters.ExtraConfig["revision"],
				"thinking_control":  configured.Parameters.ExtraConfig[chat.ExtraConfigThinkingControl],
			}
			if revision := strings.TrimSpace(configured.Parameters.ExtraConfig["model_revision"]); revision != "" {
				modelRevision = revision
			} else if revision := strings.TrimSpace(configured.Parameters.ExtraConfig["revision"]); revision != "" {
				modelRevision = revision
			}
		}
	}
	configDigest, err := artifactkey.DigestConfig(struct {
		Template    *types.PromptTemplateStructured `json:"template"`
		Temperature float64                         `json:"temperature"`
		MaxTokens   int                             `json:"max_tokens"`
		Thinking    bool                            `json:"thinking"`
		Model       map[string]any                  `json:"model"`
	}{Template: template, Temperature: 0.3, MaxTokens: 4096, Thinking: false, Model: modelConfig})
	if err != nil {
		return nil, nil, fmt.Errorf("digest graph extraction config: %w", err)
	}
	modelID := model.GetModelID()
	if modelID == "" {
		modelID = modelRevision
	}
	key := graphExtractArtifactKey(tenantID, inputDigest, modelID, modelRevision, configDigest)
	timing := s.graphArtifactTiming()
	deadline := time.Now().Add(timing.Wait)
	for {
		owner, tokenErr := newArtifactOwnerToken()
		if tokenErr != nil {
			return nil, nil, tokenErr
		}
		claim, claimErr := s.artifactRepo.Claim(ctx, interfaces.ArtifactClaim{
			TenantID: tenantID, ArtifactKey: key, ArtifactKind: graphExtractArtifactKind,
			InputDigest: inputDigest, ModelID: modelID, ModelRevision: modelRevision,
			PromptVersion: graphExtractPromptVersion, ConfigDigest: configDigest,
			ProducerVersion: graphExtractProducerVersion, OwnerToken: owner,
			LeaseDuration: timing.Lease,
		})
		if claimErr != nil {
			if errors.Is(claimErr, interfaces.ErrArtifactCorrupt) {
				logger.Warnf(ctx, "graph extraction cache: corrupt repository artifact; computing without cache")
				return recomputeGraphAfterCacheError(ctx, model, template, content, inputDigest)
			}
			return nil, nil, fmt.Errorf("claim graph extraction artifact: %w", claimErr)
		}
		switch claim.Outcome {
		case interfaces.ArtifactClaimHit:
			graph, decodeErr := decodeGraphExtractArtifact(claim.Artifact)
			if decodeErr != nil {
				logger.Warnf(ctx, "graph extraction cache: invalid artifact; computing without cache: %v", decodeErr)
				return recomputeGraphAfterCacheError(ctx, model, template, content, inputDigest)
			}
			return graph, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheHit, true), nil
		case interfaces.ArtifactClaimBusy:
			if time.Now().After(deadline) {
				return nil, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheFailed, false), fmt.Errorf("graph extraction artifact busy wait timed out")
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(timing.Poll):
			}
		case interfaces.ArtifactClaimClaimed:
			return s.computeGraphExtractArtifact(ctx, tenantID, key, owner, inputDigest, model, template, content)
		default:
			return nil, nil, fmt.Errorf("unknown graph extraction artifact claim outcome %q", claim.Outcome)
		}
	}
}

func (s *ChunkExtractService) computeGraphExtractArtifact(ctx context.Context, tenantID uint64, key, owner, inputDigest string, model chat.Chat, template *types.PromptTemplateStructured, content string) (*types.GraphData, types.JSONMap, error) {
	timing := s.graphArtifactTiming()
	heartbeatCtx, stop := context.WithCancel(ctx)
	var mu sync.Mutex
	var leaseErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(timing.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case now := <-ticker.C:
				if err := s.artifactRepo.RenewLease(heartbeatCtx, tenantID, key, owner, now, timing.Lease); err != nil {
					mu.Lock()
					leaseErr = err
					mu.Unlock()
					return
				}
			}
		}
	}()
	graph, observation, computeErr := extractGraphWithObservation(ctx, model, template, content)
	stop()
	<-done
	mu.Lock()
	lost := leaseErr
	mu.Unlock()
	if lost != nil {
		if !errors.Is(lost, interfaces.ErrArtifactLostOwnership) {
			s.failGraphExtractArtifact(ctx, tenantID, key, owner, "lease_renew_failed", "graph extraction artifact lease renewal failed")
		}
		markGraphCacheFailureObservation(observation, model, content, inputDigest)
		return nil, observation, fmt.Errorf("graph extraction artifact lease: %w", lost)
	}
	if computeErr != nil || graph == nil {
		s.failGraphExtractArtifact(ctx, tenantID, key, owner, "graph_extract_failed", "graph extraction provider or parser failed")
		requestCount, batchCount := observation["request_count"], observation["batch_count"]
		mergeObservationOutput(observation, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheFailed, false))
		observation["request_count"], observation["batch_count"] = requestCount, batchCount
		if computeErr == nil {
			computeErr = fmt.Errorf("graph extraction produced no graph")
		}
		return graph, observation, computeErr
	}
	// Keep the artifact at the pure extraction boundary. In particular, do not
	// let chunk bindings (which belong to the current graph-store write) or the
	// source text leak into the reusable candidate result.
	graph, err := graphExtractCandidate(graph)
	if err != nil {
		s.failGraphExtractArtifact(ctx, tenantID, key, owner, "invalid_graph", "graph extraction produced an invalid candidate graph")
		markGraphCacheFailureObservation(observation, model, content, inputDigest)
		return nil, observation, err
	}
	payload, err := json.Marshal(graphExtractArtifactPayload{SchemaVersion: graphExtractArtifactSchemaVersion, Graph: graph})
	if err != nil {
		s.failGraphExtractArtifact(ctx, tenantID, key, owner, "encode_failed", "graph extraction artifact encoding failed")
		markGraphCacheFailureObservation(observation, model, content, inputDigest)
		return nil, observation, fmt.Errorf("encode graph extraction artifact: %w", err)
	}
	if err := s.artifactRepo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: tenantID, ArtifactKey: key, OwnerToken: owner, Payload: payload, PayloadEncoding: "json", PayloadDigest: artifactkey.DigestBytes(payload)}); err != nil {
		if !errors.Is(err, interfaces.ErrArtifactLostOwnership) {
			s.failGraphExtractArtifact(ctx, tenantID, key, owner, "complete_failed", "graph extraction artifact completion failed")
		}
		markGraphCacheFailureObservation(observation, model, content, inputDigest)
		return nil, observation, fmt.Errorf("complete graph extraction artifact: %w", err)
	}
	requestCount, batchCount := observation["request_count"], observation["batch_count"]
	mergeObservationOutput(observation, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheComputed, true))
	// Preserve the real provider counts collected during the miss.
	observation["request_count"], observation["batch_count"] = requestCount, batchCount
	return graph, observation, nil
}

func (s *ChunkExtractService) failGraphExtractArtifact(ctx context.Context, tenantID uint64, key, owner, code, message string) {
	failCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		failCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), s.graphArtifactTiming().Cleanup)
	}
	defer cancel()
	if err := s.artifactRepo.Fail(failCtx, interfaces.ArtifactFailure{TenantID: tenantID, ArtifactKey: key, OwnerToken: owner, ErrorCode: code, ErrorMessage: message}); err != nil && !errors.Is(err, interfaces.ErrArtifactLostOwnership) {
		logger.Warnf(ctx, "graph extraction cache: fail artifact: %v", err)
	}
}

func decodeGraphExtractArtifact(a *types.DerivedArtifact) (*types.GraphData, error) {
	if a == nil || a.PayloadEncoding != "json" {
		return nil, interfaces.ErrArtifactCorrupt
	}
	var payload graphExtractArtifactPayload
	if err := json.Unmarshal(a.Payload, &payload); err != nil || payload.SchemaVersion != graphExtractArtifactSchemaVersion || payload.Graph == nil {
		return nil, interfaces.ErrArtifactCorrupt
	}
	return graphExtractCandidate(payload.Graph)
}

// graphExtractCandidate copies only the provider/parser result that is stable
// across source chunks. Chunk IDs and GraphData.Text are contextual data added
// outside the cached extraction boundary.
func graphExtractCandidate(graph *types.GraphData) (*types.GraphData, error) {
	if graph == nil {
		return nil, interfaces.ErrArtifactCorrupt
	}
	candidate := &types.GraphData{
		Node:     make([]*types.GraphNode, 0, len(graph.Node)),
		Relation: make([]*types.GraphRelation, 0, len(graph.Relation)),
	}
	nodes := make(map[string]*types.GraphNode, len(graph.Node))
	for _, node := range graph.Node {
		if node == nil {
			return nil, interfaces.ErrArtifactCorrupt
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			return nil, interfaces.ErrArtifactCorrupt
		}
		current := nodes[name]
		if current == nil {
			current = &types.GraphNode{Name: name}
			nodes[name] = current
		}
		current.Attributes = append(current.Attributes, node.Attributes...)
	}
	for _, node := range nodes {
		node.Attributes = uniqueSortedStrings(node.Attributes)
		candidate.Node = append(candidate.Node, node)
	}
	sort.Slice(candidate.Node, func(i, j int) bool { return candidate.Node[i].Name < candidate.Node[j].Name })
	relations := make(map[string]*types.GraphRelation, len(graph.Relation))
	for _, relation := range graph.Relation {
		if relation == nil {
			return nil, interfaces.ErrArtifactCorrupt
		}
		r := &types.GraphRelation{Node1: strings.TrimSpace(relation.Node1), Node2: strings.TrimSpace(relation.Node2), Type: strings.TrimSpace(relation.Type)}
		if r.Node1 == "" || r.Node2 == "" {
			return nil, interfaces.ErrArtifactCorrupt
		}
		relations[r.Node1+"\x00"+r.Node2+"\x00"+r.Type] = r
	}
	for _, relation := range relations {
		candidate.Relation = append(candidate.Relation, relation)
	}
	sort.Slice(candidate.Relation, func(i, j int) bool {
		a, b := candidate.Relation[i], candidate.Relation[j]
		if a.Node1 != b.Node1 {
			return a.Node1 < b.Node1
		}
		if a.Node2 != b.Node2 {
			return a.Node2 < b.Node2
		}
		return a.Type < b.Type
	})
	return candidate, nil
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func recomputeGraphAfterCacheError(ctx context.Context, model chat.Chat, template *types.PromptTemplateStructured, content, inputDigest string) (*types.GraphData, types.JSONMap, error) {
	graph, observation, err := extractGraphWithObservation(ctx, model, template, content)
	requestCount, batchCount := observation["request_count"], observation["batch_count"]
	mergeObservationOutput(observation, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheFailed, err == nil))
	observation["cache_status"] = string(types.IngestionCacheStatusError)
	observation["request_count"], observation["batch_count"] = requestCount, batchCount
	if err != nil {
		return graph, observation, err
	}
	graph, normalizeErr := graphExtractCandidate(graph)
	if normalizeErr != nil {
		return nil, observation, normalizeErr
	}
	return graph, observation, nil
}

func markGraphCacheFailureObservation(observation types.JSONMap, model chat.Chat, content, inputDigest string) {
	requestCount, batchCount := observation["request_count"], observation["batch_count"]
	mergeObservationOutput(observation, graphExtractCacheObservation(model, content, inputDigest, types.ArtifactCacheFailed, false))
	observation["cache_status"] = string(types.IngestionCacheStatusError)
	observation["request_count"], observation["batch_count"] = requestCount, batchCount
}

func graphExtractCacheObservation(model chat.Chat, content, inputDigest string, event types.ArtifactCacheEvent, success bool) types.JSONMap {
	o := types.ArtifactObservation(types.IngestionOperationGraphExtractChunk, graphExtractArtifactKind, inputDigest[:12], event)
	o.Stage = types.StagePostProcess
	o.ModelID = model.GetModelID()
	o.ModelType = "chat"
	o.TotalItems = 1
	o.InputChars = len(content)
	o.Success = success
	return o.ToJSONMap()
}
