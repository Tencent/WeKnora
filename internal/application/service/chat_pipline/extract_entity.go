package chatpipline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginExtractEntity is a plugin for extracting entities from user queries
// It uses historical dialog context and large language models to identify key entities in the user's original query
type PluginExtractEntity struct {
	modelService interfaces.ModelService         // Model service for calling large language models
	template     *types.PromptTemplateStructured // Template for generating prompts
}

// NewPluginRewrite creates a new query rewriting plugin instance
// Also registers the plugin with the event manager
func NewPluginExtractEntity(
	eventManager *EventManager,
	modelService interfaces.ModelService,
	config *config.Config,
) *PluginExtractEntity {
	res := &PluginExtractEntity{
		modelService: modelService,
		template:     config.ExtractEntity,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to
// This plugin only responds to REWRITE_QUERY events
func (p *PluginExtractEntity) ActivationEvents() []types.EventType {
	return []types.EventType{types.REWRITE_QUERY}
}

// OnEvent processes triggered events
// When receiving a REWRITE_QUERY event, it rewrites the user query using conversation history and the language model
func (p *PluginExtractEntity) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	query := chatManage.Query

	model, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get model, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	extractor := NewExtractor(model, p.template)
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

type Extractor struct {
	chat     chat.Chat
	formater *Formater
	template *types.PromptTemplateStructured
	chatOpt  *chat.ChatOptions
}

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

func (e *Extractor) Extract(ctx context.Context, content string) (*types.GraphData, error) {
	generator := NewQAPromptGenerator(e.formater, e.template)
	logger.Debugf(ctx, "chat response: %s", generator.Render(ctx, content))
	chatResponse, err := e.chat.Chat(ctx, []chat.Message{
		{
			Role:    "user",
			Content: generator.Render(ctx, content),
		},
	}, e.chatOpt)
	if err != nil {
		logger.Errorf(ctx, "failed to chat: %v", err)
		return nil, err
	}

	graph, err := e.formater.ParseGraph(ctx, chatResponse.Content)
	if err != nil {
		logger.Errorf(ctx, "failed to parse graph: %v", err)
		return nil, err
	}
	return graph, nil
}

type QAPromptGenerator struct {
	Formater        *Formater
	Template        *types.PromptTemplateStructured
	ExamplesHeading string
	QuestionHeading string
	QuestionPrefix  string
	answerPrefix    string
}

func NewQAPromptGenerator(formater *Formater, template *types.PromptTemplateStructured) *QAPromptGenerator {
	return &QAPromptGenerator{
		Formater:        formater,
		Template:        template,
		ExamplesHeading: "# Examples",
		QuestionHeading: "# Question",
		QuestionPrefix:  "Q: ",
		answerPrefix:    "A: ",
	}
}

func (qa *QAPromptGenerator) formatExampleAsText(example types.GraphData) (string, error) {
	question := example.Text
	answer, err := qa.Formater.formatExtraction(example.Node, example.Relation)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s\n%s%s\n", qa.QuestionPrefix, question, qa.answerPrefix, answer), nil
}

func (qa *QAPromptGenerator) Render(ctx context.Context, question string) string {
	promptLines := []string{fmt.Sprintf("%s\n", qa.Template.Description)}

	if len(qa.Template.Examples) > 0 {
		promptLines = append(promptLines, qa.ExamplesHeading)
		for _, example := range qa.Template.Examples {
			formatted, err := qa.formatExampleAsText(example)
			if err != nil {
				return ""
			}
			promptLines = append(promptLines, formatted)
		}
	}
	promptLines = append(promptLines, qa.QuestionHeading)
	promptLines = append(promptLines, fmt.Sprintf("%s%s", qa.QuestionPrefix, question))
	promptLines = append(promptLines, qa.answerPrefix)
	return strings.Join(promptLines, "\n")
}

type FormatType string

const (
	FormatTypeJSON FormatType = "json"
	FormatTypeYAML FormatType = "yaml"
)

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

type Formater struct {
	attributeSuffix string
	formatType      FormatType
	useFences       bool
	nodePrefix      string

	relationSource string
	relationTarget string
	relationPrefix string
}

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
			f.relationSource: relation.Source.Name,
			f.relationTarget: relation.Target.Name,
			f.relationPrefix: relation.Type,
		}
		if len(relation.Attributes) > 0 {
			item[fmt.Sprintf("%s%s", f.relationPrefix, f.attributeSuffix)] = relation.Attributes
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

func (f *Formater) parseOutput(ctx context.Context, text string) ([]map[string]interface{}, error) {
	if text == "" {
		return nil, errors.New("Empty or invalid input string.")
	}
	content := f.extractContent(ctx, text)

	var parsed interface{}
	var err error
	if f.formatType == FormatTypeJSON {
		err = json.Unmarshal([]byte(content), &parsed)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to parse %s content: %s", strings.ToUpper(string(f.formatType)), err.Error())
	}
	if parsed == nil {
		return nil, fmt.Errorf("Content must be a list of extractions or a dict.")
	}

	var items []interface{}
	if parsedMap, ok := parsed.(map[string]interface{}); ok {
		items = []interface{}{parsedMap}
	} else if parsedList, ok := parsed.([]interface{}); ok {
		items = parsedList
	} else {
		return nil, fmt.Errorf("Expected list or dict, got %T", parsed)
	}

	itemsList := make([]map[string]interface{}, 0)
	for _, item := range items {
		if itemMap, ok := item.(map[string]interface{}); ok {
			itemsList = append(itemsList, itemMap)
		} else {
			return nil, fmt.Errorf("Each item in the sequence must be a mapping.")
		}
	}
	return itemsList, nil
}

func (f *Formater) ParseGraph(ctx context.Context, text string) (*types.GraphData, error) {
	matchData, err := f.parseOutput(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(matchData) == 0 {
		logger.Debugf(ctx, "Received empty extraction data.")
		return &types.GraphData{}, nil
	}
	// mm, _ := json.Marshal(matchData)
	// logger.Debugf(ctx, "Parsed graph data: %s", string(mm))

	var nodes []*types.GraphNode
	var relations []*types.GraphRelation

	for _, group := range matchData {
		switch {
		case group[f.nodePrefix] != nil:
			var attributes map[string]string
			attributesKey := f.nodePrefix + f.attributeSuffix
			if attr, ok := group[attributesKey].(map[string]interface{}); ok {
				attributes = make(map[string]string)
				for k, v := range attr {
					attributes[k] = fmt.Sprintf("%v", v)
				}
			}
			nodes = append(nodes, &types.GraphNode{
				Name:       fmt.Sprintf("%v", group[f.nodePrefix]),
				Attributes: attributes,
			})
		case group[f.relationSource] != nil && group[f.relationTarget] != nil:
			var attributes map[string]string
			attributesKey := f.relationPrefix + f.attributeSuffix
			if attr, ok := group[attributesKey].(map[string]interface{}); ok {
				attributes = make(map[string]string)
				for k, v := range attr {
					attributes[k] = fmt.Sprintf("%v", v)
				}
			}
			relations = append(relations, &types.GraphRelation{
				Source:     &types.GraphNode{Name: fmt.Sprintf("%v", group[f.relationSource])},
				Target:     &types.GraphNode{Name: fmt.Sprintf("%v", group[f.relationTarget])},
				Type:       fmt.Sprintf("%v", group[f.relationPrefix]),
				Attributes: attributes,
			})
		default:
			logger.Warnf(ctx, "Unsupported graph group: %v", group)
			continue
		}
	}
	graph := &types.GraphData{
		Node:     nodes,
		Relation: relations,
	}
	f.rebuildGraph(ctx, graph)
	return graph, nil
}

func (f *Formater) rebuildGraph(ctx context.Context, graph *types.GraphData) {
	nodeMap := make(map[string]*types.GraphNode)
	nodes := make([]*types.GraphNode, 0, len(graph.Node))
	for _, node := range graph.Node {
		if prenode, ok := nodeMap[node.Name]; ok {
			logger.Infof(ctx, "Duplicate node ID: %s, merge attribute", node.Name)
			if node.Attributes == nil {
				node.Attributes = make(map[string]string)
			}
			for k, v := range prenode.Attributes {
				node.Attributes[k] = v
			}
			continue
		}
		nodeMap[node.Name] = node
		nodes = append(nodes, node)
	}
	relations := make([]*types.GraphRelation, 0, len(graph.Relation))
	for _, relation := range graph.Relation {
		if relation.Source.Name == relation.Target.Name {
			logger.Infof(ctx, "Duplicate relation, ignore it")
			continue
		}
		if _, ok := nodeMap[relation.Source.Name]; !ok {
			node := &types.GraphNode{Name: relation.Source.Name}
			nodes = append(nodes, node)
			nodeMap[relation.Source.Name] = node
			logger.Infof(ctx, "Add unknown source node ID: %s", relation.Source.Name)
		}
		source := nodeMap[relation.Source.Name]
		if _, ok := nodeMap[relation.Target.Name]; !ok {
			node := &types.GraphNode{Name: relation.Target.Name}
			nodes = append(nodes, node)
			nodeMap[relation.Target.Name] = node
			logger.Infof(ctx, "Add unknown target node ID: %s", relation.Target.Name)
		}
		target := nodeMap[relation.Target.Name]

		if relation.Type == "" {
			relation.Type = f.relationPrefix
		}
		relation.Source = source
		relation.Target = target
		relations = append(relations, relation)
	}
	*graph = types.GraphData{
		Node:     nodes,
		Relation: relations,
	}
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
		logger.Warnf(ctx, "no match found")
		return strings.TrimSpace(text)
	}
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
