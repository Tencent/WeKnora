package chatpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginExtractEntity is a plugin for extracting entities from user queries
// It uses historical dialog context and large language models to identify key entities in the user's original query
type PluginExtractEntity struct {
	modelService      interfaces.ModelService         // Model service for calling large language models
	template          *types.PromptTemplateStructured // Template for generating prompts
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository
	knowledgeService  interfaces.KnowledgeService // For shared KB document resolution
	knowledgeRepo     interfaces.KnowledgeRepository
}

// NewPluginExtractEntity creates a new extract-entity plugin instance
// Also registers the plugin with the event manager
func NewPluginExtractEntity(
	eventManager *EventManager,
	modelService interfaces.ModelService,
	knowledgeBaseRepo interfaces.KnowledgeBaseRepository,
	knowledgeService interfaces.KnowledgeService,
	knowledgeRepo interfaces.KnowledgeRepository,
	config *config.Config,
) *PluginExtractEntity {
	res := &PluginExtractEntity{
		modelService:      modelService,
		template:          config.ExtractManager.ExtractEntity,
		knowledgeBaseRepo: knowledgeBaseRepo,
		knowledgeService:  knowledgeService,
		knowledgeRepo:     knowledgeRepo,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to.
func (p *PluginExtractEntity) ActivationEvents() []types.EventType {
	return []types.EventType{types.QUERY_UNDERSTAND}
}

// OnEvent processes triggered events
// When receiving a QUERY_UNDERSTAND event, it extracts entities from the query
func (p *PluginExtractEntity) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Debugf(ctx, "skipping extract entity, neo4j is disabled")
		return next()
	}

	query := chatManage.Query

	model, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get model, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	// Collect all knowledge base IDs to query
	kbIDSet := make(map[string]struct{})
	for _, id := range chatManage.KnowledgeBaseIDs {
		kbIDSet[id] = struct{}{}
	}

	// If KnowledgeIDs is specified, retrieve them and collect their knowledge base IDs (include shared KB docs)
	// Also build a mapping from KnowledgeID to KnowledgeBaseID
	knowledgeToKBMap := make(map[string]string)
	if len(chatManage.KnowledgeIDs) > 0 {
		knowledges, err := p.knowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, chatManage.TenantID, chatManage.KnowledgeIDs)
		if err != nil {
			logger.Errorf(ctx, "failed to get knowledges: %v", err)
			return next()
		}
		for _, k := range knowledges {
			kbIDSet[k.KnowledgeBaseID] = struct{}{}
			knowledgeToKBMap[k.ID] = k.KnowledgeBaseID
		}
	}

	// Convert set to slice
	allKBIDs := make([]string, 0, len(kbIDSet))
	for id := range kbIDSet {
		allKBIDs = append(allKBIDs, id)
	}

	// Batch retrieve all knowledge bases
	kbs, err := p.knowledgeBaseRepo.GetKnowledgeBaseByIDs(ctx, allKBIDs)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge bases: %v", err)
		return next()
	}

	// Check if any knowledge base has ExtractConfig enabled and collect their IDs
	enabledKBSet := make(map[string]struct{})
	for _, kb := range kbs {
		if kb.ExtractConfig != nil && kb.ExtractConfig.Enabled {
			enabledKBSet[kb.ID] = struct{}{}
		}
	}
	if len(enabledKBSet) == 0 {
		logger.Debugf(ctx, "no knowledge base has extract config enabled")
		return next()
	}

	// Save enabled knowledge base IDs for later use in search_entity
	enabledKBIDs := make([]string, 0, len(enabledKBSet))
	for id := range enabledKBSet {
		enabledKBIDs = append(enabledKBIDs, id)
	}
	chatManage.EntityKBIDs = enabledKBIDs

	// Filter knowledgeToKBMap to only include files from enabled knowledge bases
	entityKnowledge := make(map[string]string)
	for knowledgeID, kbID := range knowledgeToKBMap {
		if _, ok := enabledKBSet[kbID]; ok {
			entityKnowledge[knowledgeID] = kbID
		}
	}
	chatManage.EntityKnowledge = entityKnowledge

	template := &types.PromptTemplateStructured{
		Description: p.template.Description,
		Examples:    p.template.Examples,
	}
	extractor := NewExtractor(model, template)
	graph, err := extractor.Extract(ctx, query)
	if err != nil {
		logger.Errorf(ctx, "Failed to extract entities, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	nodes := []string{}
	for _, node := range graph.Node {
		nodes = append(nodes, node.Name)
	}
	logger.Debugf(ctx, "extracted node: %v", nodes)
	chatManage.Entity = nodes
	return next()
}

// Extractor is a struct for extracting entities
type Extractor struct {
	chat     chat.Chat
	formater *Formater
	template *types.PromptTemplateStructured
	chatOpt  *chat.ChatOptions
}

// NewExtractor creates a new extractor
func NewExtractor(
	chatModel chat.Chat,
	template *types.PromptTemplateStructured,
) Extractor {
	think := false
	return Extractor{
		chat:     chatModel,
		formater: NewFormater(),
		template: template,
		chatOpt: &chat.ChatOptions{
			Temperature: 0.3,
			MaxTokens:   4096,
			Thinking:    &think,
		},
	}
}

// Extract extracts entities from content
func (e *Extractor) Extract(ctx context.Context, content string) (*types.GraphData, error) {
	generator := NewQAPromptGenerator(e.formater, e.template)

	// logger.Debugf(ctx, "chat system: %s", generator.System(ctx))
	// logger.Debugf(ctx, "chat user: %s", generator.User(ctx, content))

	modelCtx := types.WithLLMCallMetadata(ctx, "entity_extraction", "")
	chatResponse, err := e.chat.Chat(modelCtx, generator.Render(ctx, content), e.chatOpt)
	if err != nil {
		logger.Errorf(ctx, "failed to chat: %v", err)
		return nil, err
	}

	graph, err := e.formater.ParseGraph(ctx, chatResponse.Content)
	if err != nil {
		logger.Errorf(ctx, "failed to parse graph: %v", err)
		return nil, err
	}
	// e.RemoveUnknownRelation(ctx, graph)
	return graph, nil
}

// RemoveUnknownRelation removes unknown relations from graph
func (e *Extractor) RemoveUnknownRelation(ctx context.Context, graph *types.GraphData) {
	relationType := make(map[string]bool)
	for _, tag := range e.template.Tags {
		relationType[tag] = true
	}

	relationNew := make([]*types.GraphRelation, 0)
	for _, relation := range graph.Relation {
		if _, ok := relationType[relation.Type]; ok {
			relationNew = append(relationNew, relation)
		} else {
			logger.Infof(ctx, "Unknown relation type %s with %v, ignore it", relation.Type, e.template.Tags)
		}
	}
	graph.Relation = relationNew
}

// QAPromptGenerator is a struct for generating QA prompts
type QAPromptGenerator struct {
	Formater        *Formater
	Template        *types.PromptTemplateStructured
	ExamplesHeading string
	QuestionHeading string
	QuestionPrefix  string
	AnswerPrefix    string
}

// NewQAPromptGenerator creates a new QA prompt generator
func NewQAPromptGenerator(formater *Formater, template *types.PromptTemplateStructured) *QAPromptGenerator {
	return &QAPromptGenerator{
		Formater:        formater,
		Template:        template,
		ExamplesHeading: "# Examples",
		QuestionHeading: "# Question",
		QuestionPrefix:  "Q: ",
		AnswerPrefix:    "A: ",
	}
}

// System generates a system prompt
func (qa *QAPromptGenerator) System(ctx context.Context) string {
	promptLines := []string{}

	if len(qa.Template.Tags) == 0 {
		promptLines = append(promptLines, qa.Template.Description)
	} else {
		tags, _ := json.Marshal(qa.Template.Tags)
		promptLines = append(promptLines, fmt.Sprintf(qa.Template.Description, string(tags)))
	}
	if len(qa.Template.Examples) > 0 {
		promptLines = append(promptLines, qa.ExamplesHeading)
		for _, example := range qa.Template.Examples {
			// Question
			promptLines = append(promptLines, fmt.Sprintf("%s%s", qa.QuestionPrefix, strings.TrimSpace(example.Text)))

			// Answer
			answer, err := qa.Formater.formatExtraction(example.Node, example.Relation)
			if err != nil {
				return ""
			}
			promptLines = append(promptLines, fmt.Sprintf("%s%s", qa.AnswerPrefix, answer))

			// new line
			promptLines = append(promptLines, "")
		}
	}
	return strings.Join(promptLines, "\n")
}

// User generates a user prompt
func (qa *QAPromptGenerator) User(ctx context.Context, question string) string {
	promptLines := []string{}
	promptLines = append(promptLines, qa.QuestionHeading)
	promptLines = append(promptLines, fmt.Sprintf("%s%s", qa.QuestionPrefix, question))
	promptLines = append(promptLines, qa.AnswerPrefix)
	return strings.Join(promptLines, "\n")
}

// Render renders a prompt
func (qa *QAPromptGenerator) Render(ctx context.Context, question string) []chat.Message {
	return []chat.Message{
		{
			Role:    "system",
			Content: qa.System(ctx),
		},
		{
			Role:    "user",
			Content: qa.User(ctx, question),
		},
	}
}

// FormatType is a type for format types
type FormatType string

const (
	// FormatTypeJSON is a format type for JSON
	FormatTypeJSON FormatType = "json"
	// FormatTypeYAML is a format type for YAML
	FormatTypeYAML FormatType = "yaml"
)

// maxGraphIdentifierRunes bounds entity names and relationship types before
// they reach the graph database. The limit is counted in Unicode code points,
// not bytes, so Chinese and other multi-byte names are handled consistently.
const maxGraphIdentifierRunes = 255

const (
	_FENCE_START   = "```"
	_LANGUAGE_TAG  = `(?P<lang>[A-Za-z0-9_+-]+)?`
	_FENCE_NEWLINE = `(?:\s*\n)?`
	_FENCE_BODY    = `(?P<body>[\s\S]*?)`
	_FENCE_END     = "```"
)

var _FENCE_RE = regexp.MustCompile(
	_FENCE_START + _LANGUAGE_TAG + _FENCE_NEWLINE + _FENCE_BODY + _FENCE_END,
)

// Formater is a struct for formatting entities
type Formater struct {
	attributeSuffix string
	formatType      FormatType
	useFences       bool
	nodePrefix      string

	relationSource string
	relationTarget string
	relationPrefix string
}

// NewFormater creates a new formater
func NewFormater() *Formater {
	return &Formater{
		attributeSuffix: "_attributes",
		formatType:      FormatTypeJSON,
		useFences:       true,
		nodePrefix:      "entity",
		relationSource:  "entity1",
		relationTarget:  "entity2",
		relationPrefix:  "relation",
	}
}

// formatExtraction formats extraction
func (f *Formater) formatExtraction(nodes []*types.GraphNode, relations []*types.GraphRelation) (string, error) {
	items := make([]map[string]interface{}, 0)
	for _, node := range nodes {
		item := map[string]interface{}{
			f.nodePrefix: node.Name,
		}
		if len(node.Attributes) > 0 {
			item[fmt.Sprintf("%s%s", f.nodePrefix, f.attributeSuffix)] = node.Attributes
		}
		items = append(items, item)
	}
	for _, relation := range relations {
		item := map[string]interface{}{
			f.relationSource: relation.Node1,
			f.relationTarget: relation.Node2,
			f.relationPrefix: relation.Type,
		}
		items = append(items, item)
	}
	formatted := ""
	switch f.formatType {
	default:
		formattedBytes, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return "", err
		}
		formatted = string(formattedBytes)
	}
	if f.useFences {
		formatted = f.addFences(formatted)
	}
	return formatted, nil
}

type parsedGraphItem struct {
	index int
	data  map[string]interface{}
}

type parsedGraphOutput struct {
	items         []parsedGraphItem
	itemsReceived int
	failures      []types.GraphFailureDetail
}

func (f *Formater) parseOutput(ctx context.Context, text string) (*parsedGraphOutput, error) {
	if text == "" {
		return nil, errors.New("empty or invalid input string")
	}
	content := f.extractContent(ctx, text)
	// logger.Debugf(ctx, "Extracted content: %s", content)
	if content == "" {
		return nil, errors.New("empty or invalid input string")
	}

	var parsed interface{}
	var err error
	if f.formatType == FormatTypeJSON {
		err = json.Unmarshal([]byte(content), &parsed)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s content: %s", strings.ToUpper(string(f.formatType)), err.Error())
	}
	if parsed == nil {
		return nil, fmt.Errorf("content must be a list of extractions or a dict")
	}

	var items []interface{}
	if parsedMap, ok := parsed.(map[string]interface{}); ok {
		items = []interface{}{parsedMap}
	} else if parsedList, ok := parsed.([]interface{}); ok {
		items = parsedList
	} else {
		return nil, fmt.Errorf("expected list or dict, got %T", parsed)
	}

	result := &parsedGraphOutput{
		items:         make([]parsedGraphItem, 0, len(items)),
		itemsReceived: len(items),
	}
	for i, item := range items {
		if itemMap, ok := item.(map[string]interface{}); ok {
			result.items = append(result.items, parsedGraphItem{index: i, data: itemMap})
		} else {
			// A single malformed extraction must not discard the other valid
			// nodes and relationships in the same LLM response.
			logger.Warnf(ctx, "Reject graph item %d: expected object, got %T", i, item)
			result.failures = append(result.failures, types.GraphFailureDetail{
				Stage:     "validation",
				Kind:      "item",
				ItemIndex: i,
				Reason:    fmt.Sprintf("expected object, got %T", item),
			})
		}
	}
	return result, nil
}

// normalizeGraphIdentifier validates the scalar fields that become graph
// identifiers: entity names, relationship endpoints, and relationship types.
// It deliberately does not apply the configured relationship allow-list;
// allow-list validation is a separate policy decision.
func normalizeGraphIdentifier(value interface{}) (string, string) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Sprintf("must be a string, got %T", value)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "must not be empty"
	}
	if len([]rune(s)) > maxGraphIdentifierRunes {
		return "", fmt.Sprintf("must not exceed %d characters", maxGraphIdentifierRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Sprintf("must not contain control character %U", r)
		}
	}
	return s, ""
}

// normalizeGraphAttributes keeps only non-empty string attributes and removes
// duplicates within one node. Invalid attributes do not invalidate an
// otherwise valid node because attributes are optional enrichment data.
func normalizeGraphAttributes(value interface{}) []string {
	raw, ok := value.([]interface{})
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	attributes := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		attributes = append(attributes, s)
	}
	return attributes
}

func (f *Formater) ParseGraph(ctx context.Context, text string) (*types.GraphData, error) {
	parsed, err := f.parseOutput(ctx, text)
	if err != nil {
		return nil, err
	}
	diagnostics := &types.GraphDiagnostics{
		ItemsReceived: parsed.itemsReceived,
		ItemsRejected: len(parsed.failures),
	}
	for _, failure := range parsed.failures {
		diagnostics.AddFailure(failure)
	}
	// mm, _ := json.Marshal(matchData)
	// logger.Debugf(ctx, "Parsed graph data: %s", string(mm))

	var nodes []*types.GraphNode
	var relations []*types.GraphRelation

	for _, item := range parsed.items {
		i := item.index
		group := item.data
		_, hasNode := group[f.nodePrefix]
		_, hasRelationSource := group[f.relationSource]
		_, hasRelationTarget := group[f.relationTarget]
		_, hasRelationType := group[f.relationPrefix]

		switch {
		case hasNode:
			diagnostics.NodesExtracted++
			name, reason := normalizeGraphIdentifier(group[f.nodePrefix])
			if reason != "" {
				logger.Warnf(ctx, "Reject graph node item %d: entity %s", i, reason)
				diagnostics.ItemsRejected++
				diagnostics.NodesRejected++
				diagnostics.AddFailure(types.GraphFailureDetail{
					Stage: "validation", Kind: "node", ItemIndex: i, Reason: "entity " + reason,
				})
				continue
			}
			attributesKey := f.nodePrefix + f.attributeSuffix
			nodes = append(nodes, &types.GraphNode{
				Name:       name,
				Attributes: normalizeGraphAttributes(group[attributesKey]),
			})
		case hasRelationSource || hasRelationTarget || hasRelationType:
			diagnostics.RelationsExtracted++
			source, sourceReason := normalizeGraphIdentifier(group[f.relationSource])
			target, targetReason := normalizeGraphIdentifier(group[f.relationTarget])
			relationType, typeReason := normalizeGraphIdentifier(group[f.relationPrefix])
			if sourceReason != "" || targetReason != "" || typeReason != "" {
				logger.Warnf(ctx,
					"Reject graph relation item %d: entity1 %s; entity2 %s; relation %s",
					i, validationReason(sourceReason), validationReason(targetReason), validationReason(typeReason))
				diagnostics.ItemsRejected++
				diagnostics.RelationsRejected++
				diagnostics.AddFailure(types.GraphFailureDetail{
					Stage: "validation", Kind: "relation", ItemIndex: i,
					Node1: source, Node2: target, Type: relationType,
					Reason: fmt.Sprintf("entity1 %s; entity2 %s; relation %s",
						validationReason(sourceReason), validationReason(targetReason), validationReason(typeReason)),
				})
				continue
			}
			if source == target {
				logger.Warnf(ctx, "Reject graph relation item %d: self-relation %q", i, source)
				diagnostics.ItemsRejected++
				diagnostics.RelationsRejected++
				diagnostics.AddFailure(types.GraphFailureDetail{
					Stage: "validation", Kind: "relation", ItemIndex: i,
					Node1: source, Node2: target, Type: relationType,
					Reason: "self-relation is not allowed",
				})
				continue
			}
			relations = append(relations, &types.GraphRelation{
				Node1: source,
				Node2: target,
				Type:  relationType,
			})
		default:
			logger.Warnf(ctx, "Reject graph item %d: unsupported fields", i)
			diagnostics.ItemsRejected++
			diagnostics.AddFailure(types.GraphFailureDetail{
				Stage: "validation", Kind: "item", ItemIndex: i, Reason: "unsupported fields",
			})
			continue
		}
	}
	graph := &types.GraphData{
		Node:        nodes,
		Relation:    relations,
		Diagnostics: diagnostics,
	}
	f.rebuildGraph(ctx, graph, diagnostics)
	diagnostics.NodesAccepted = len(graph.Node)
	diagnostics.RelationsAccepted = len(graph.Relation)
	if diagnostics.ItemsRejected > 0 {
		logger.Warnf(ctx, "Graph validation rejected %d item(s); retained %d node(s) and %d relation(s)",
			diagnostics.ItemsRejected, len(graph.Node), len(graph.Relation))
	} else if parsed.itemsReceived == 0 {
		logger.Debugf(ctx, "received empty extraction data.")
	}
	return graph, nil
}

func validationReason(reason string) string {
	if reason == "" {
		return "valid"
	}
	return reason
}

// mergeGraphNodeAttributes preserves the first-seen attribute order and
// appends only attributes not already present on the retained node.
func mergeGraphNodeAttributes(existing, incoming []string) []string {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	merged := make([]string, 0, len(existing)+len(incoming))
	for _, attribute := range existing {
		if _, ok := seen[attribute]; ok {
			continue
		}
		seen[attribute] = struct{}{}
		merged = append(merged, attribute)
	}
	for _, attribute := range incoming {
		if _, ok := seen[attribute]; ok {
			continue
		}
		seen[attribute] = struct{}{}
		merged = append(merged, attribute)
	}
	return merged
}

func (f *Formater) rebuildGraph(ctx context.Context, graph *types.GraphData, diagnostics *types.GraphDiagnostics) {
	nodeMap := make(map[string]*types.GraphNode)
	nodes := make([]*types.GraphNode, 0, len(graph.Node))
	for _, node := range graph.Node {
		if existingNode, ok := nodeMap[node.Name]; ok {
			logger.Infof(ctx, "Duplicate node ID: %s, merge attributes", node.Name)
			existingNode.Attributes = mergeGraphNodeAttributes(existingNode.Attributes, node.Attributes)
			diagnostics.NodesMerged++
			continue
		}
		nodeMap[node.Name] = node
		nodes = append(nodes, node)
	}

	relations := make([]*types.GraphRelation, 0, len(graph.Relation))
	relationSeen := make(map[string]struct{}, len(graph.Relation))
	for relationIndex, relation := range graph.Relation {
		if relation.Node1 == relation.Node2 {
			logger.Infof(ctx, "Self-relation %q ignored", relation.Node1)
			diagnostics.ItemsRejected++
			diagnostics.RelationsRejected++
			diagnostics.AddFailure(types.GraphFailureDetail{
				Stage: "validation", Kind: "relation", ItemIndex: relationIndex,
				Node1: relation.Node1, Node2: relation.Node2, Type: relation.Type,
				Reason: "self-relation is not allowed",
			})
			continue
		}

		_, sourceExists := nodeMap[relation.Node1]
		_, targetExists := nodeMap[relation.Node2]
		if !sourceExists || !targetExists {
			reason := ""
			switch {
			case !sourceExists && !targetExists:
				reason = fmt.Sprintf("source %q and target %q are not extracted nodes", relation.Node1, relation.Node2)
				logger.Warnf(ctx, "Relation ignored: source %q and target %q are not extracted nodes",
					relation.Node1, relation.Node2)
			case !sourceExists:
				reason = fmt.Sprintf("source %q is not an extracted node", relation.Node1)
				logger.Warnf(ctx, "Relation ignored: source %q is not an extracted node", relation.Node1)
			default:
				reason = fmt.Sprintf("target %q is not an extracted node", relation.Node2)
				logger.Warnf(ctx, "Relation ignored: target %q is not an extracted node", relation.Node2)
			}
			diagnostics.ItemsRejected++
			diagnostics.RelationsRejected++
			diagnostics.AddFailure(types.GraphFailureDetail{
				Stage: "validation", Kind: "relation", ItemIndex: relationIndex,
				Node1: relation.Node1, Node2: relation.Node2, Type: relation.Type, Reason: reason,
			})
			continue
		}
		relationKey := relation.Node1 + "\x00" + relation.Type + "\x00" + relation.Node2
		if _, exists := relationSeen[relationKey]; exists {
			logger.Infof(ctx, "Duplicate relation ignored: %s --[%s]--> %s",
				relation.Node1, relation.Type, relation.Node2)
			diagnostics.RelationsMerged++
			continue
		}
		relationSeen[relationKey] = struct{}{}

		relations = append(relations, relation)
	}
	graph.Node = nodes
	graph.Relation = relations
}

func (f *Formater) extractContent(ctx context.Context, text string) string {
	if !f.useFences {
		return strings.TrimSpace(text)
	}
	validTags := map[FormatType]map[string]struct{}{
		FormatTypeYAML: {"yaml": {}, "yml": {}},
		FormatTypeJSON: {"json": {}},
	}
	matches := _FENCE_RE.FindAllStringSubmatch(text, -1)
	var candidates []string
	for _, match := range matches {
		lang := match[1]
		body := match[2]
		if f.isValidLanguageTag(lang, validTags) {
			candidates = append(candidates, body)
		}
	}
	switch {
	case len(candidates) == 1:
		return strings.TrimSpace(candidates[0])

	case len(candidates) > 1:
		logger.Warnf(ctx, "multiple candidates found: %d", len(candidates))
		return strings.TrimSpace(candidates[0])

	case len(matches) == 1:
		logger.Debugf(ctx, "no candidate found, use first match without language tag: %s", matches[0][1])
		return strings.TrimSpace(matches[0][2])

	case len(matches) > 1:
		logger.Warnf(ctx, "multiple matches found: %d", len(matches))
		return strings.TrimSpace(matches[0][2])

	default:
		// Fallback strategies for cases where the fence regex fails to match.
		// This commonly happens when:
		//   1. The LLM output is truncated (no closing fence) — issue #1113 Pattern 3.
		//   2. The opening fence is malformed or surrounded by unexpected content,
		//      so the non-greedy regex falls back to the raw text — issue #1113 Pattern 1.
		// Without these fallbacks, the raw text (including backticks) is passed to
		// json.Unmarshal and fails with `invalid character '`'`.
		if extracted := stripFencesAndExtract(text, f.formatType); extracted != "" {
			logger.Debugf(ctx, "no fence match, recovered content via fallback (%d bytes)", len(extracted))
			return extracted
		}
		logger.Warnf(ctx, "no match found")
		return strings.TrimSpace(text)
	}
}

// stripFencesAndExtract attempts to recover a parseable payload from an LLM
// response when the strict fence regex fails. It handles three common cases:
//
//  1. Truncated responses with an opening ```lang fence but no closing fence
//     (LLM hit max_tokens mid-output).
//  2. Responses where the JSON/YAML body is preceded or followed by prose
//     and the fences are present but malformed.
//  3. Responses with no fences at all but a recognizable JSON object/array
//     embedded in surrounding text.
//
// It returns an empty string when no plausible payload can be recovered, so
// callers can fall back to their own behavior.
func stripFencesAndExtract(text string, format FormatType) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	// Case 1: opening fence present (with or without language tag) but no
	// matching closing fence. Take everything after the first fence and
	// strip any trailing backticks.
	if idx := strings.Index(trimmed, "```"); idx >= 0 {
		rest := trimmed[idx+3:]
		// Drop optional language tag on the same line.
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			firstLine := strings.TrimSpace(rest[:nl])
			// A pure language tag is short and alphanumeric-ish.
			if firstLine == "" || isLikelyLanguageTag(firstLine) {
				rest = rest[nl+1:]
			}
		}
		// If there is a closing fence somewhere, cut at it.
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		rest = strings.TrimSpace(rest)
		rest = strings.Trim(rest, "`")
		rest = strings.TrimSpace(rest)
		if rest != "" {
			return rest
		}
	}

	// Case 2: no usable fence found, but the payload may still contain a
	// JSON object/array. Extract the outermost {...} or [...] substring.
	if format == FormatTypeJSON {
		if extracted := extractJSONLike(trimmed); extracted != "" {
			return extracted
		}
	}

	return ""
}

// isLikelyLanguageTag reports whether s looks like a markdown fence language
// tag (e.g. "json", "yaml", "yml", "go"). It must be short and contain only
// characters typical for a language identifier.
func isLikelyLanguageTag(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
}

// extractJSONLike returns the outermost JSON object or array substring from s,
// or an empty string if none is found. It picks whichever bracket type appears
// first in the input, which mirrors what the LLM is most likely to have
// produced. The returned slice is not validated as JSON; callers must still
// json.Unmarshal it.
func extractJSONLike(s string) string {
	objStart := strings.IndexByte(s, '{')
	arrStart := strings.IndexByte(s, '[')
	var open, closeCh byte
	var start int
	switch {
	case objStart < 0 && arrStart < 0:
		return ""
	case objStart < 0:
		open, closeCh, start = '[', ']', arrStart
	case arrStart < 0:
		open, closeCh, start = '{', '}', objStart
	case objStart < arrStart:
		open, closeCh, start = '{', '}', objStart
	default:
		open, closeCh, start = '[', ']', arrStart
	}
	// Find matching close, respecting string literals so braces/brackets
	// inside JSON strings don't unbalance the count.
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[start : i+1])
			}
		}
	}
	return ""
}

func (f *Formater) addFences(content string) string {
	content = strings.TrimSpace(content)
	return fmt.Sprintf("```%s\n%s\n```", f.formatType, content)
}

func (f *Formater) isValidLanguageTag(lang string, validTags map[FormatType]map[string]struct{}) bool {
	if lang == "" {
		return true
	}
	tag := strings.TrimSpace(strings.ToLower(lang))
	validSet, ok := validTags[f.formatType]
	if !ok {
		return false
	}
	_, exists := validSet[tag]
	return exists
}
