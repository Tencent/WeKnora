package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	graphArtifactStage         = "chat.graph-extraction"
	graphArtifactKeyVersion    = uint16(1)
	graphArtifactCodecVersion  = uint8(1)
	graphArtifactPromptVersion = "graph-extraction-v1"
	graphArtifactCanonicalizer = "graph-data-v1"
)

type graphArtifactRequest struct {
	tenantID             uint64
	model                chat.Chat
	modelRevision        string
	messages             []chat.Message
	options              *chat.ChatOptions
	promptVersion        string
	canonicalizerVersion string
}

func newGraphArtifactKey(request graphArtifactRequest) (types.ProcessingArtifactKey, error) {
	if len(request.messages) != 2 ||
		request.messages[0].Role != "system" || strings.TrimSpace(request.messages[0].Content) == "" ||
		request.messages[1].Role != "user" || strings.TrimSpace(request.messages[1].Content) == "" {
		return types.ProcessingArtifactKey{}, errors.New("graph artifact extraction messages are incomplete")
	}
	return newChatArtifactKey(chatArtifactRequest{
		tenantID:             request.tenantID,
		stage:                graphArtifactStage,
		keyVersion:           graphArtifactKeyVersion,
		model:                request.model,
		modelRevision:        request.modelRevision,
		messages:             request.messages,
		options:              request.options,
		promptVersion:        request.promptVersion,
		canonicalizerVersion: request.canonicalizerVersion,
	})
}

type graphArtifactValue struct {
	Version   uint8                   `json:"version"`
	Nodes     []graphArtifactNode     `json:"nodes"`
	Relations []graphArtifactRelation `json:"relations"`
}

type graphArtifactNode struct {
	Name       string   `json:"name"`
	Attributes []string `json:"attributes"`
}

type graphArtifactRelation struct {
	Node1 string `json:"node1"`
	Node2 string `json:"node2"`
	Type  string `json:"type"`
}

func encodeGraphArtifact(graph *types.GraphData) ([]byte, error) {
	value, err := canonicalGraphArtifact(graph)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode graph artifact: %w", err)
	}
	return payload, nil
}

func decodeGraphArtifact(payload []byte, chunkID string) (*types.GraphData, error) {
	if strings.TrimSpace(chunkID) == "" || !utf8.ValidString(chunkID) {
		return nil, errors.New("graph artifact chunk ID must not be empty")
	}
	if !utf8.Valid(payload) {
		return nil, errors.New("graph artifact payload is not valid UTF-8")
	}

	var value graphArtifactValue
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode graph artifact: %w", err)
	}
	if err := ensureGraphArtifactEOF(decoder); err != nil {
		return nil, err
	}
	if value.Version != graphArtifactCodecVersion {
		return nil, fmt.Errorf("unsupported graph artifact version %d", value.Version)
	}
	if value.Nodes == nil || value.Relations == nil {
		return nil, errors.New("graph artifact payload is incomplete")
	}

	graph := &types.GraphData{
		Node:     make([]*types.GraphNode, 0, len(value.Nodes)),
		Relation: make([]*types.GraphRelation, 0, len(value.Relations)),
	}
	for _, node := range value.Nodes {
		graph.Node = append(graph.Node, &types.GraphNode{
			Name:       node.Name,
			Attributes: append([]string(nil), node.Attributes...),
		})
	}
	for _, relation := range value.Relations {
		graph.Relation = append(graph.Relation, &types.GraphRelation{
			Node1: relation.Node1,
			Node2: relation.Node2,
			Type:  relation.Type,
		})
	}
	canonical, err := canonicalGraphArtifact(graph)
	if err != nil {
		return nil, fmt.Errorf("validate graph artifact: %w", err)
	}
	return bindGraphArtifact(canonical, chunkID), nil
}

func bindGraphArtifact(value graphArtifactValue, chunkID string) *types.GraphData {
	graph := &types.GraphData{
		Node:     make([]*types.GraphNode, 0, len(value.Nodes)),
		Relation: make([]*types.GraphRelation, 0, len(value.Relations)),
	}
	for _, node := range value.Nodes {
		graph.Node = append(graph.Node, &types.GraphNode{
			Name:       node.Name,
			Chunks:     []string{chunkID},
			Attributes: append([]string(nil), node.Attributes...),
		})
	}
	for _, relation := range value.Relations {
		graph.Relation = append(graph.Relation, &types.GraphRelation{
			Node1:  relation.Node1,
			Node2:  relation.Node2,
			Type:   relation.Type,
			Chunks: []string{chunkID},
		})
	}
	return graph
}

func canonicalGraphArtifact(graph *types.GraphData) (graphArtifactValue, error) {
	if graph == nil {
		return graphArtifactValue{}, errors.New("graph artifact graph must not be nil")
	}

	value := graphArtifactValue{
		Version:   graphArtifactCodecVersion,
		Nodes:     make([]graphArtifactNode, 0, len(graph.Node)),
		Relations: make([]graphArtifactRelation, 0, len(graph.Relation)),
	}
	nodeNames := make(map[string]struct{}, len(graph.Node))
	for _, node := range graph.Node {
		if node == nil || strings.TrimSpace(node.Name) == "" || !utf8.ValidString(node.Name) {
			return graphArtifactValue{}, errors.New("graph artifact contains an invalid node")
		}
		if _, exists := nodeNames[node.Name]; exists {
			return graphArtifactValue{}, fmt.Errorf("graph artifact contains duplicate node %q", node.Name)
		}
		nodeNames[node.Name] = struct{}{}
		attributes := append([]string(nil), node.Attributes...)
		for _, attribute := range attributes {
			if !utf8.ValidString(attribute) {
				return graphArtifactValue{}, errors.New("graph artifact contains an invalid node attribute")
			}
		}
		sort.Strings(attributes)
		value.Nodes = append(value.Nodes, graphArtifactNode{Name: node.Name, Attributes: attributes})
	}
	sort.Slice(value.Nodes, func(i, j int) bool { return value.Nodes[i].Name < value.Nodes[j].Name })

	relationKeys := make(map[string]struct{}, len(graph.Relation))
	for _, relation := range graph.Relation {
		if relation == nil || strings.TrimSpace(relation.Node1) == "" ||
			strings.TrimSpace(relation.Node2) == "" || strings.TrimSpace(relation.Type) == "" ||
			!utf8.ValidString(relation.Node1) || !utf8.ValidString(relation.Node2) ||
			!utf8.ValidString(relation.Type) {
			return graphArtifactValue{}, errors.New("graph artifact contains an invalid relation")
		}
		if relation.Node1 == relation.Node2 {
			return graphArtifactValue{}, errors.New("graph artifact contains a self relation")
		}
		if _, exists := nodeNames[relation.Node1]; !exists {
			return graphArtifactValue{}, fmt.Errorf("graph artifact relation references unknown node %q", relation.Node1)
		}
		if _, exists := nodeNames[relation.Node2]; !exists {
			return graphArtifactValue{}, fmt.Errorf("graph artifact relation references unknown node %q", relation.Node2)
		}
		key := relation.Node1 + "\x00" + relation.Type + "\x00" + relation.Node2
		if _, exists := relationKeys[key]; exists {
			continue
		}
		relationKeys[key] = struct{}{}
		value.Relations = append(value.Relations, graphArtifactRelation{
			Node1: relation.Node1,
			Node2: relation.Node2,
			Type:  relation.Type,
		})
	}
	sort.Slice(value.Relations, func(i, j int) bool {
		left, right := value.Relations[i], value.Relations[j]
		if left.Node1 != right.Node1 {
			return left.Node1 < right.Node1
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		return left.Node2 < right.Node2
	})
	return value, nil
}

func ensureGraphArtifactEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("decode graph artifact: trailing JSON value")
		}
		return fmt.Errorf("decode graph artifact: %w", err)
	}
	return nil
}

func completeGraphArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	request graphArtifactRequest,
	chunkID string,
	provider func(context.Context) (*types.GraphData, error),
) (*types.GraphData, bool, bool, error) {
	if provider == nil {
		return nil, false, false, errors.New("graph artifact provider must not be nil")
	}

	if store == nil {
		graph, err := provider(ctx)
		if err != nil {
			return nil, false, true, err
		}
		canonical, err := canonicalizeGraphArtifact(graph, chunkID)
		return canonical, false, true, err
	}

	key, err := newGraphArtifactKey(request)
	if err != nil {
		return nil, false, false, err
	}
	payload, hit, err := store.Get(ctx, key)
	if err != nil {
		return nil, false, false, fmt.Errorf("get graph artifact: %w", err)
	}
	if hit {
		graph, decodeErr := decodeGraphArtifact(payload, chunkID)
		if decodeErr == nil {
			return graph, true, false, nil
		}
		if err := store.Invalidate(ctx, key, payload); err != nil {
			return nil, false, false, fmt.Errorf("invalidate graph artifact: %w", err)
		}
	}

	graph, err := provider(ctx)
	if err != nil {
		return nil, false, true, err
	}
	candidate, err := encodeGraphArtifact(graph)
	if err != nil {
		return nil, false, true, err
	}
	winner, _, err := store.PutIfAbsent(ctx, key, candidate)
	if err != nil {
		return nil, false, true, fmt.Errorf("put graph artifact: %w", err)
	}
	canonical, err := decodeGraphArtifact(winner, chunkID)
	if err != nil {
		if invalidateErr := store.Invalidate(ctx, key, winner); invalidateErr != nil {
			return nil, false, true, fmt.Errorf("invalidate graph artifact: %w", invalidateErr)
		}
		winner, _, putErr := store.PutIfAbsent(ctx, key, candidate)
		if putErr != nil {
			return nil, false, true, fmt.Errorf("put graph artifact repair: %w", putErr)
		}
		canonical, err = decodeGraphArtifact(winner, chunkID)
		if err == nil {
			return canonical, false, true, nil
		}
		canonical, err = decodeGraphArtifact(candidate, chunkID)
	}
	return canonical, false, true, err
}

func canonicalizeGraphArtifact(graph *types.GraphData, chunkID string) (*types.GraphData, error) {
	payload, err := encodeGraphArtifact(graph)
	if err != nil {
		return nil, err
	}
	return decodeGraphArtifact(payload, chunkID)
}

func extractGraphArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	tenantID uint64,
	chunkID string,
	model chat.Chat,
	modelRevision string,
	template *types.PromptTemplateStructured,
	content string,
) (*types.GraphData, bool, bool, error) {
	if model == nil {
		return nil, false, false, errors.New("graph artifact model must not be nil")
	}
	if template == nil {
		return nil, false, false, errors.New("graph artifact template must not be nil")
	}

	formatter := chatpipeline.NewFormater()
	messages := chatpipeline.NewQAPromptGenerator(formatter, template).Render(ctx, content)
	thinking := false
	options := &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   4096,
		Thinking:    &thinking,
	}
	request := graphArtifactRequest{
		tenantID:             tenantID,
		model:                model,
		modelRevision:        modelRevision,
		messages:             messages,
		options:              options,
		promptVersion:        graphArtifactPromptVersion,
		canonicalizerVersion: graphArtifactCanonicalizer,
	}
	return completeGraphArtifact(ctx, store, request, chunkID, func(ctx context.Context) (*types.GraphData, error) {
		response, err := model.Chat(ctx, messages, options)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("graph extraction model returned a nil response")
		}
		return formatter.ParseGraph(ctx, response.Content)
	})
}
