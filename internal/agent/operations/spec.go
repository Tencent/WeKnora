package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Swagger extensions that put an operation in the catalog. They are written
// as swag annotations next to the handler, so the route, parameters and
// response shape come from the same document the HTTP API is described by:
//
//	// @x-agent      {"id":"knowledge.delete","risk":"destructive","target":"knowledge.get","label":"title"}
//	// @x-agent-hint "Cannot be undone."
//	// @Param        id  path  string  true  "知识ID"  extensions(x-agent=knowledge_id)
//
// Exposure is opt-in at every level. An operation without x-agent is not in
// the catalog; a parameter without x-agent cannot be set by the model; a
// response field without x-agent (set with an `extensions:"x-agent"` struct
// tag) never reaches the model. x-agent-hint on an operation is guidance for
// the model; it replaces the human-facing description in the catalog.
const (
	// extAgent marks an operation, parameter or response field. On a
	// parameter or field its value may be a string: the model-facing name.
	extAgent = "x-agent"
	// extAgentMaximum caps an integer parameter below what the API allows,
	// to keep results within what the model context can use.
	extAgentMaximum = "x-agent-maximum"
	// extAgentKeys lists the keys of a free-form object field that are
	// projected, separated by "|".
	extAgentKeys = "x-agent-keys"
)

// agentOperation is the value of x-agent on an operation.
type agentOperation struct {
	ID   string `json:"id"`
	Risk Risk   `json:"risk"`
	// Target and Label declare the approval target of a mutation.
	Target string `json:"target"`
	Label  string `json:"label"`
	// Root overrides the projected part of the response. By default it is
	// "data" when the body has that envelope and the body itself otherwise.
	Root     *string `json:"root"`
	MaxItems int     `json:"max_items"`
	// CatchAll names the last path segment that the router registers as a
	// catch-all (*slug), which Swagger cannot express.
	CatchAll string `json:"catch_all"`
}

type specDocument struct {
	BasePath    string                                `json:"basePath"`
	Paths       map[string]map[string]json.RawMessage `json:"paths"`
	Definitions map[string]*specSchema                `json:"definitions"`
}

type specOperation struct {
	Summary     string                  `json:"summary"`
	Description string                  `json:"description"`
	Parameters  []specParameter         `json:"parameters"`
	Responses   map[string]specResponse `json:"responses"`
	Agent       json.RawMessage         `json:"x-agent"`
	Hint        string                  `json:"x-agent-hint"`
}

type specParameter struct {
	Name        string      `json:"name"`
	In          string      `json:"in"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Required    bool        `json:"required"`
	Enum        []any       `json:"enum"`
	Minimum     *float64    `json:"minimum"`
	Maximum     *float64    `json:"maximum"`
	Items       *specSchema `json:"items"`
	Schema      *specSchema `json:"schema"`
	Agent       any         `json:"x-agent"`
	AgentMax    string      `json:"x-agent-maximum"`
}

type specResponse struct {
	Schema *specSchema `json:"schema"`
}

type specSchema struct {
	Ref         string                 `json:"$ref"`
	Type        string                 `json:"type"`
	Description string                 `json:"description"`
	Items       *specSchema            `json:"items"`
	Properties  map[string]*specSchema `json:"properties"`
	Required    []string               `json:"required"`
	AllOf       []*specSchema          `json:"allOf"`
	Enum        []any                  `json:"enum"`
	Minimum     *float64               `json:"minimum"`
	Maximum     *float64               `json:"maximum"`
	Agent       any                    `json:"x-agent"`
	AgentKeys   string                 `json:"x-agent-keys"`
	AgentMax    string                 `json:"x-agent-maximum"`
}

var specMethods = []string{"get", "post", "put", "patch", "delete"}

// FromSpec compiles the operations a Swagger 2.0 document marks with
// x-agent. The result still has to pass NewCatalog, which owns the safety
// rules (forbidden areas, risk versus method, approval targets).
func FromSpec(document []byte) ([]Operation, error) {
	var doc specDocument
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("parse swagger document: %w", err)
	}
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var ops []Operation
	for _, path := range paths {
		for _, method := range specMethods {
			raw, ok := doc.Paths[path][method]
			if !ok {
				continue
			}
			var sop specOperation
			if err := json.Unmarshal(raw, &sop); err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			if len(sop.Agent) == 0 {
				continue
			}
			op, err := doc.compile(strings.ToUpper(method), path, sop)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func (doc *specDocument) compile(method, path string, sop specOperation) (Operation, error) {
	var meta agentOperation
	dec := json.NewDecoder(bytes.NewReader(sop.Agent))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		return Operation{}, fmt.Errorf("%s: %w", extAgent, err)
	}
	route, err := ginRoute(doc.BasePath, path, meta.CatchAll)
	if err != nil {
		return Operation{}, fmt.Errorf("%s: %w", extAgent, err)
	}
	op := Operation{
		ID:          meta.ID,
		Summary:     strings.TrimSpace(sop.Summary),
		Description: strings.TrimSpace(sop.Hint),
		Risk:        meta.Risk,
		Method:      method,
		Route:       route,
	}
	if op.Description == "" {
		op.Description = strings.TrimSpace(sop.Description)
	}
	if meta.Target != "" || meta.Label != "" {
		if meta.Target == "" || meta.Label == "" {
			return Operation{}, fmt.Errorf("%s: target and label go together", extAgent)
		}
		op.Target = &Target{Operation: meta.Target, Label: meta.Label}
	}

	for _, sp := range sop.Parameters {
		params, err := doc.params(sp)
		if err != nil {
			return Operation{}, fmt.Errorf("param %q: %w", sp.Name, err)
		}
		op.Params = append(op.Params, params...)
	}

	result, err := doc.result(sop.Responses, meta)
	if err != nil {
		return Operation{}, err
	}
	op.Result = result
	return op, nil
}

// ginRoute turns "/knowledge/{id}" under basePath into "/api/v1/knowledge/:id".
// catchAll, when set, names the last segment, which becomes "*name".
func ginRoute(basePath, path, catchAll string) (string, error) {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := seg[1 : len(seg)-1]
			if name == catchAll {
				if i != len(segments)-1 {
					return "", fmt.Errorf("catch_all %q must be the last path segment", catchAll)
				}
				segments[i] = "*" + name
				continue
			}
			segments[i] = ":" + name
		}
	}
	route := strings.TrimRight(basePath, "/") + strings.Join(segments, "/")
	if catchAll != "" && !strings.HasSuffix(route, "/*"+catchAll) {
		return "", fmt.Errorf("catch_all %q is not a path segment", catchAll)
	}
	return route, nil
}

// agentName reports whether ext exposes an element and under which name.
func agentName(ext any, fallback string) (string, bool, error) {
	switch v := ext.(type) {
	case nil:
		return "", false, nil
	case bool:
		return fallback, v, nil
	case string:
		if v == "" {
			return "", false, fmt.Errorf("%s needs a name", extAgent)
		}
		return v, true, nil
	default:
		return "", false, fmt.Errorf("%s must be true or a name", extAgent)
	}
}

func (doc *specDocument) params(sp specParameter) ([]Param, error) {
	if sp.In == string(InBody) {
		return doc.bodyParams(sp)
	}
	name, exposed, err := agentName(sp.Agent, sp.Name)
	if err != nil {
		return nil, err
	}
	if !exposed {
		if sp.Required {
			return nil, fmt.Errorf("required by the API but not exposed with %s", extAgent)
		}
		return nil, nil
	}
	if sp.In != string(InPath) && sp.In != string(InQuery) {
		return nil, fmt.Errorf("%s parameters are not supported", sp.In)
	}
	p := Param{
		Name:        name,
		In:          Location(sp.In),
		Key:         sp.Name,
		Required:    sp.Required,
		Description: strings.TrimSpace(sp.Description),
	}
	shape := &specSchema{Type: sp.Type, Items: sp.Items, Enum: sp.Enum, Minimum: sp.Minimum, Maximum: sp.Maximum}
	if err := doc.typeParam(&p, shape, sp.AgentMax); err != nil {
		return nil, err
	}
	return []Param{p}, nil
}

// bodyParams exposes the top-level fields of a JSON body that carry
// x-agent; the model sets them as flat parameters.
func (doc *specDocument) bodyParams(sp specParameter) ([]Param, error) {
	if sp.Schema == nil {
		return nil, nil
	}
	obj, err := doc.object(sp.Schema)
	if err != nil {
		return nil, err
	}
	required := map[string]bool{}
	for _, key := range obj.Required {
		required[key] = true
	}
	var out []Param
	for _, key := range sortedKeys(obj.Properties) {
		prop := obj.Properties[key]
		name, exposed, err := agentName(prop.Agent, key)
		if err != nil {
			return nil, fmt.Errorf("body field %q: %w", key, err)
		}
		if !exposed {
			if sp.Required && required[key] {
				return nil, fmt.Errorf("body field %q is required but not exposed with %s", key, extAgent)
			}
			continue
		}
		p := Param{
			Name:        name,
			In:          InBody,
			Key:         key,
			Required:    sp.Required && required[key],
			Description: strings.TrimSpace(prop.Description),
		}
		shape, err := doc.resolve(prop)
		if err != nil {
			return nil, err
		}
		if err := doc.typeParam(&p, shape, prop.AgentMax); err != nil {
			return nil, fmt.Errorf("body field %q: %w", key, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (doc *specDocument) typeParam(p *Param, shape *specSchema, agentMax string) error {
	switch shape.Type {
	case "string":
		p.Type = TypeString
		p.Enum = enumStrings(shape.Enum)
	case "integer":
		p.Type = TypeInteger
	case "boolean":
		p.Type = TypeBoolean
	case "array":
		items, err := doc.resolve(shape.Items)
		if err != nil {
			return err
		}
		if items == nil || items.Type != "string" {
			return fmt.Errorf("only arrays of strings are supported")
		}
		p.Type = TypeStringList
		p.Enum = enumStrings(items.Enum)
	default:
		return fmt.Errorf("unsupported type %q", shape.Type)
	}
	if p.Type != TypeInteger {
		if agentMax != "" {
			return fmt.Errorf("%s applies to integers only", extAgentMaximum)
		}
		return nil
	}
	return boundInteger(p, shape.Minimum, shape.Maximum, agentMax)
}

func boundInteger(p *Param, minimum, maximum *float64, agentMax string) error {
	if minimum == nil && maximum == nil && agentMax == "" {
		return nil
	}
	lower, upper := 0, math.MaxInt32
	if minimum != nil {
		lower = int(*minimum)
	}
	if maximum != nil {
		upper = int(*maximum)
	}
	if agentMax != "" {
		limit, err := strconv.Atoi(agentMax)
		if err != nil || limit < lower || limit > upper {
			return fmt.Errorf("%s %q must be an integer within the API bounds", extAgentMaximum, agentMax)
		}
		upper = limit
	}
	p.Minimum, p.Maximum = lower, upper
	return nil
}

func enumStrings(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func (doc *specDocument) result(responses map[string]specResponse, meta agentOperation) (Result, error) {
	var body *specSchema
	for _, code := range []string{"200", "201", "202"} {
		if r, ok := responses[code]; ok && r.Schema != nil {
			body = r.Schema
			break
		}
	}
	if body == nil {
		return Result{}, fmt.Errorf("no success response schema")
	}
	top, err := doc.resolve(body)
	if err != nil {
		return Result{}, err
	}

	root := ""
	switch {
	case meta.Root != nil:
		root = *meta.Root
	case top.Type == "object" && top.Properties["data"] != nil:
		root = "data"
	}
	out := Result{Root: root, MaxItems: meta.MaxItems}
	target := top
	for _, key := range splitPath(root) {
		if target.Type != "object" || target.Properties[key] == nil {
			return Result{}, fmt.Errorf("response has no %q", root)
		}
		if target, err = doc.resolve(target.Properties[key]); err != nil {
			return Result{}, err
		}
	}
	if target.Type == "array" {
		out.List = true
		if target, err = doc.resolve(target.Items); err != nil {
			return Result{}, err
		}
	}
	if target == nil || target.Type != "object" {
		return Result{}, fmt.Errorf("response %q is not an object or a list of objects", root)
	}
	if out.Fields, err = doc.fields(target, ""); err != nil {
		return Result{}, err
	}
	if len(out.Fields) == 0 {
		return Result{}, fmt.Errorf("response %q exposes no fields; tag them with %s", root, extAgent)
	}
	if root != "" {
		envelope := *top
		envelope.Properties = map[string]*specSchema{}
		for key, prop := range top.Properties {
			if key != splitPath(root)[0] {
				envelope.Properties[key] = prop
			}
		}
		if out.Meta, err = doc.fields(&envelope, ""); err != nil {
			return Result{}, err
		}
	}
	return out, nil
}

// fields lists the projected fields of obj. Nested objects marked with
// x-agent are flattened: their marked fields are named on their own.
func (doc *specDocument) fields(obj *specSchema, prefix string) ([]Field, error) {
	var out []Field
	for _, key := range sortedKeys(obj.Properties) {
		raw := obj.Properties[key]
		name, exposed, err := agentName(raw.Agent, key)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", prefix+key, err)
		}
		if !exposed {
			continue
		}
		from := prefix + key
		prop, err := doc.resolve(raw)
		if err != nil {
			return nil, err
		}
		switch {
		case isScalar(prop.Type):
			out = append(out, Field{From: from, As: name})
		case prop.Type == "array":
			items, err := doc.resolve(prop.Items)
			if err != nil {
				return nil, err
			}
			if items == nil || !isScalar(items.Type) {
				return nil, fmt.Errorf("field %q: only lists of scalars are projected; "+
					"point the operation's root at a list of objects instead", from)
			}
			out = append(out, Field{From: from, As: name})
		case len(prop.Properties) > 0:
			if name != key {
				return nil, fmt.Errorf("field %q: objects are flattened, so name their fields instead", from)
			}
			nested, err := doc.fields(prop, from+".")
			if err != nil {
				return nil, err
			}
			if len(nested) == 0 {
				return nil, fmt.Errorf("field %q: object exposes no fields", from)
			}
			out = append(out, nested...)
		case raw.AgentKeys != "":
			for _, sub := range strings.Split(raw.AgentKeys, "|") {
				if sub = strings.TrimSpace(sub); sub != "" {
					out = append(out, Field{From: from + "." + sub, As: sub})
				}
			}
		default:
			return nil, fmt.Errorf("field %q: free-form object needs %s", from, extAgentKeys)
		}
	}
	return out, nil
}

func isScalar(t string) bool {
	return t == "string" || t == "integer" || t == "number" || t == "boolean"
}

// resolve follows $ref and merges allOf into one schema. Extensions on the
// referencing schema stay with the caller; the merged result carries the
// structure only.
func (doc *specDocument) resolve(s *specSchema) (*specSchema, error) {
	return doc.resolveDepth(s, 0)
}

const maxSchemaDepth = 32

func (doc *specDocument) resolveDepth(s *specSchema, depth int) (*specSchema, error) {
	if s == nil {
		return nil, nil
	}
	if depth > maxSchemaDepth {
		return nil, fmt.Errorf("schema nesting is too deep")
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/definitions/")
		def, ok := doc.Definitions[name]
		if !ok {
			return nil, fmt.Errorf("undefined schema %q", s.Ref)
		}
		return doc.resolveDepth(def, depth+1)
	}
	if len(s.AllOf) == 0 {
		return s, nil
	}
	merged := &specSchema{Type: s.Type, Description: s.Description, Properties: map[string]*specSchema{}}
	for key, prop := range s.Properties {
		merged.Properties[key] = prop
	}
	for _, part := range s.AllOf {
		r, err := doc.resolveDepth(part, depth+1)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		if merged.Type == "" || (merged.Type == "object" && r.Type != "") {
			merged.Type = r.Type
		}
		if r.Items != nil {
			merged.Items = r.Items
		}
		if r.Enum != nil {
			merged.Enum = r.Enum
		}
		merged.Required = append(merged.Required, r.Required...)
		for key, prop := range r.Properties {
			merged.Properties[key] = overlay(merged.Properties[key], prop)
		}
	}
	if len(merged.Properties) == 0 {
		merged.Properties = nil
	}
	return merged, nil
}

// overlay lets a composed property (DataResponse{data=X}) replace the base
// property's type while keeping the base's extensions.
func overlay(base, override *specSchema) *specSchema {
	if base == nil {
		return override
	}
	out := *override
	if out.Agent == nil {
		out.Agent = base.Agent
	}
	if out.AgentKeys == "" {
		out.AgentKeys = base.AgentKeys
	}
	return &out
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}

func sortedKeys(m map[string]*specSchema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (doc *specDocument) object(s *specSchema) (*specSchema, error) {
	obj, err := doc.resolve(s)
	if err != nil {
		return nil, err
	}
	if obj == nil || obj.Type != "object" {
		return nil, fmt.Errorf("body is not an object")
	}
	return obj, nil
}
