package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
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
	// to keep results within what the model context can use, or the length
	// of a string list.
	extAgentMaximum = "x-agent-maximum"
	// extAgentMaxBytes raises the string-parameter byte cap up to
	// maxAgentStringBytes. The default stays maxStringParamBytes.
	extAgentMaxBytes = "x-agent-max-bytes"
	// extAgentMaxRunes raises how much of one response string is projected,
	// up to maxAgentFieldRunes. The default stays maxProjectedStringRunes.
	extAgentMaxRunes = "x-agent-max-runes"
	// extAgentKeys lists the keys of a free-form object field that are
	// projected, separated by "|".
	extAgentKeys = "x-agent-keys"
	// extAgentDetail limits a response field to operations that return a
	// single object, so a long field (a prompt, say) does not repeat on
	// every item of a list.
	extAgentDetail = "x-agent-detail"
	// extAgentResponse keeps a field in responses and out of request
	// parameters. Identifiers and computed counts are projected when the
	// model reads an object, and are not something it may set on create.
	extAgentResponse = "x-agent-response"
)

// agentItems is ItemTarget as written in x-agent:
// {"param":"knowledge_ids","target":"knowledge.get","as":"knowledge_id","label":"title"}.
type agentItems struct {
	Param  string `json:"param"`
	Target string `json:"target"`
	As     string `json:"as"`
	Label  string `json:"label"`
}

// agentOperation is the value of x-agent on an operation.
type agentOperation struct {
	ID   string `json:"id"`
	Risk Risk   `json:"risk"`
	// Target and Label declare the approval target of a mutation.
	Target                string `json:"target"`
	Label                 string `json:"label"`
	RequireCompleteTarget bool   `json:"require_complete_target,omitempty"`
	// LabelParam labels the approval of a creation that has no target.
	LabelParam string `json:"label_param,omitempty"`
	// Items also resolves each item of a batch for the approval.
	Items *agentItems `json:"items,omitempty"`
	// Root overrides the projected part of the response. By default it is
	// "data" when the body has that envelope and the body itself otherwise.
	Root     *string `json:"root"`
	MaxItems int     `json:"max_items"`
	// CatchAll names the last path segment that the router registers as a
	// catch-all (*slug), which Swagger cannot express.
	CatchAll string `json:"catch_all"`
	// Role is the lowest workspace role the route accepts, when it differs
	// from the default for the risk.
	Role        types.TenantRole `json:"role,omitempty"`
	SystemAdmin bool             `json:"system_admin,omitempty"`
	// AlwaysConfirm asks before every call of a write; see Operation.
	AlwaysConfirm bool `json:"always_confirm,omitempty"`
	// Method picks one route when the handler's doc lists several (a legacy
	// GET next to the POST, say); the others stay out of the catalog.
	Method string `json:"method,omitempty"`
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
	Name          string      `json:"name"`
	In            string      `json:"in"`
	Type          string      `json:"type"`
	Description   string      `json:"description"`
	Required      bool        `json:"required"`
	Enum          []any       `json:"enum"`
	Minimum       *float64    `json:"minimum"`
	Maximum       *float64    `json:"maximum"`
	Items         *specSchema `json:"items"`
	Schema        *specSchema `json:"schema"`
	Agent         any         `json:"x-agent"`
	AgentMax      string      `json:"x-agent-maximum"`
	AgentMaxBytes string      `json:"x-agent-max-bytes"`
	// AgentRequired makes an API-optional field mandatory for the model.
	AgentRequired bool `json:"x-agent-required"`
}

type specResponse struct {
	Schema *specSchema `json:"schema"`
}

type specSchema struct {
	Ref           string                 `json:"$ref"`
	Type          string                 `json:"type"`
	Description   string                 `json:"description"`
	Items         *specSchema            `json:"items"`
	Properties    map[string]*specSchema `json:"properties"`
	Required      []string               `json:"required"`
	AllOf         []*specSchema          `json:"allOf"`
	Enum          []any                  `json:"enum"`
	Minimum       *float64               `json:"minimum"`
	Maximum       *float64               `json:"maximum"`
	Agent         any                    `json:"x-agent"`
	AgentKeys     string                 `json:"x-agent-keys"`
	AgentMax      string                 `json:"x-agent-maximum"`
	AgentMaxBytes string                 `json:"x-agent-max-bytes"`
	AgentMaxRunes string                 `json:"x-agent-max-runes"`
	AgentDetail   bool                   `json:"x-agent-detail"`
	// AgentResponse projects the field and does not accept it as a parameter.
	AgentResponse bool `json:"x-agent-response"`
	// AgentRequired makes an API-optional field mandatory for the model.
	AgentRequired bool `json:"x-agent-required"`
}

var specMethods = []string{"get", "post", "put", "patch", "delete"}

// FromSpec compiles the operations a Swagger 2.0 document marks with
// x-agent. The result still has to pass NewCatalog, which owns the safety
// rules (allowed API areas, risk versus method, approval targets).
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
			op, skip, err := doc.compile(strings.ToUpper(method), path, sop)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			if !skip {
				ops = append(ops, op)
			}
		}
	}
	return ops, nil
}

// compile builds the operation for one route, or reports skip when the
// annotation names another of the handler's methods.
func (doc *specDocument) compile(method, path string, sop specOperation) (Operation, bool, error) {
	var meta agentOperation
	dec := json.NewDecoder(bytes.NewReader(sop.Agent))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		return Operation{}, false, fmt.Errorf("%s: %w", extAgent, err)
	}
	if meta.Method != "" {
		if !slices.Contains(specMethods, strings.ToLower(meta.Method)) {
			return Operation{}, false, fmt.Errorf("%s: unknown method %q", extAgent, meta.Method)
		}
		if !strings.EqualFold(meta.Method, method) {
			return Operation{}, true, nil
		}
	}
	op, err := doc.compileOperation(method, path, sop, meta)
	return op, false, err
}

func (doc *specDocument) compileOperation(
	method, path string, sop specOperation, meta agentOperation,
) (Operation, error) {
	route, err := ginRoute(doc.BasePath, path, meta.CatchAll)
	if err != nil {
		return Operation{}, fmt.Errorf("%s: %w", extAgent, err)
	}
	op := Operation{
		ID:            meta.ID,
		Summary:       strings.TrimSpace(sop.Summary),
		Description:   strings.TrimSpace(sop.Hint),
		Risk:          meta.Risk,
		Method:        method,
		Route:         route,
		Role:          meta.Role,
		SystemAdmin:   meta.SystemAdmin,
		LabelParam:    meta.LabelParam,
		AlwaysConfirm: meta.AlwaysConfirm,
	}
	if op.Description == "" {
		op.Description = strings.TrimSpace(sop.Description)
	}
	if meta.Target != "" || meta.Label != "" || meta.RequireCompleteTarget {
		if meta.Target == "" || meta.Label == "" {
			return Operation{}, fmt.Errorf("%s: target and label go together", extAgent)
		}
		op.Target = &Target{Operation: meta.Target, Label: meta.Label, RequireComplete: meta.RequireCompleteTarget}
	}
	if items := meta.Items; items != nil {
		if op.Target == nil {
			return Operation{}, fmt.Errorf("%s: items needs a target", extAgent)
		}
		if items.Param == "" || items.Target == "" || items.As == "" || items.Label == "" {
			return Operation{}, fmt.Errorf("%s: items needs param, target, as and label", extAgent)
		}
		op.Target.Items = &ItemTarget{Param: items.Param, Operation: items.Target, As: items.As, Label: items.Label}
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
		Required:    sp.Required || sp.AgentRequired,
		AllowEmpty:  sp.AgentRequired && !sp.Required,
		Description: strings.TrimSpace(sp.Description),
	}
	shape := &specSchema{Type: sp.Type, Items: sp.Items, Enum: sp.Enum, Minimum: sp.Minimum, Maximum: sp.Maximum}
	if err := doc.typeParam(&p, shape, sp.AgentMax, sp.AgentMaxBytes); err != nil {
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
		shape, err := doc.resolve(prop)
		if err != nil {
			return nil, err
		}
		// An object is not one parameter. Its tagged scalars are, and the
		// wire key keeps the path so the request nests them again.
		if shape != nil && shape.Type != "array" && (shape.Type == "object" || len(shape.Properties) > 0) {
			nested, err := doc.flattenBodyObject(shape, key, sp.Required)
			if err != nil {
				return nil, err
			}
			if len(nested) > 0 {
				out = append(out, nested...)
				continue
			}
		}
		if prop.AgentResponse {
			if sp.Required && required[key] {
				return nil, fmt.Errorf("body field %q is required but marked %s", key, extAgentResponse)
			}
			continue
		}
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
			Required:    (sp.Required && required[key]) || prop.AgentRequired,
			AllowEmpty:  prop.AgentRequired && !required[key],
			Description: strings.TrimSpace(prop.Description),
		}
		if err := doc.typeParam(&p, shape, prop.AgentMax, prop.AgentMaxBytes); err != nil {
			return nil, fmt.Errorf("body field %q: %w", key, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// flattenBodyObject turns the tagged scalars inside an object into body
// parameters. The wire key is the dotted JSON path.
func (doc *specDocument) flattenBodyObject(obj *specSchema, prefix string, bodyRequired bool) ([]Param, error) {
	required := map[string]bool{}
	for _, key := range obj.Required {
		required[key] = true
	}
	var out []Param
	for _, key := range sortedKeys(obj.Properties) {
		prop := obj.Properties[key]
		path := prefix + "." + key
		shape, err := doc.resolve(prop)
		if err != nil {
			return nil, err
		}
		if shape != nil && shape.Type != "array" && (shape.Type == "object" || len(shape.Properties) > 0) {
			nested, err := doc.flattenBodyObject(shape, path, bodyRequired)
			if err != nil {
				return nil, err
			}
			if len(nested) > 0 {
				out = append(out, nested...)
				continue
			}
		}
		if prop.AgentResponse {
			if bodyRequired && required[key] {
				return nil, fmt.Errorf("body field %q is required but marked %s", path, extAgentResponse)
			}
			continue
		}
		name, exposed, err := agentName(prop.Agent, key)
		if err != nil {
			return nil, fmt.Errorf("body field %q: %w", path, err)
		}
		if !exposed {
			if bodyRequired && required[key] {
				return nil, fmt.Errorf("body field %q is required but not exposed with %s", path, extAgent)
			}
			continue
		}
		p := Param{
			Name:        name,
			In:          InBody,
			Key:         path,
			Required:    (bodyRequired && required[key]) || prop.AgentRequired,
			AllowEmpty:  prop.AgentRequired && !required[key],
			Description: strings.TrimSpace(prop.Description),
		}
		if err := doc.typeParam(&p, shape, prop.AgentMax, prop.AgentMaxBytes); err != nil {
			return nil, fmt.Errorf("body field %q: %w", path, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (doc *specDocument) typeParam(p *Param, shape *specSchema, agentMax, agentMaxBytes string) error {
	switch shape.Type {
	case "string":
		p.Type = TypeString
		p.Enum = enumStrings(shape.Enum)
	case "integer":
		p.Type = TypeInteger
	case "number":
		p.Type = TypeNumber
	case "boolean":
		p.Type = TypeBoolean
	case "array":
		items, err := doc.resolve(shape.Items)
		if err != nil {
			return err
		}
		switch {
		case items != nil && items.Type == "string":
			p.Type = TypeStringList
			p.Enum = enumStrings(items.Enum)
		case items != nil && items.Type == "integer":
			p.Type = TypeIntegerList
		default:
			return fmt.Errorf("only arrays of strings or integers are supported")
		}
	default:
		return fmt.Errorf("unsupported type %q", shape.Type)
	}
	if p.Type == TypeString || p.Type == TypeStringList {
		if err := boundString(p, agentMaxBytes); err != nil {
			return err
		}
	} else if agentMaxBytes != "" {
		return fmt.Errorf("%s applies to strings only", extAgentMaxBytes)
	}
	switch p.Type {
	case TypeInteger:
		return boundInteger(p, shape.Minimum, shape.Maximum, agentMax)
	case TypeStringList, TypeIntegerList:
		if agentMax == "" {
			return nil
		}
		limit, err := strconv.Atoi(agentMax)
		if err != nil || limit < 1 || limit > defaultMaxListItems {
			return fmt.Errorf("%s %q must be between 1 and %d items", extAgentMaximum, agentMax, defaultMaxListItems)
		}
		p.MaxItems = limit
		return nil
	}
	if agentMax != "" {
		return fmt.Errorf("%s applies to integers and lists only", extAgentMaximum)
	}
	return nil
}

func boundString(p *Param, agentMaxBytes string) error {
	if agentMaxBytes == "" {
		return nil
	}
	limit, err := strconv.Atoi(agentMaxBytes)
	if err != nil || limit < 1 || limit > maxAgentStringBytes {
		return fmt.Errorf("%s %q must be an integer from 1 to %d", extAgentMaxBytes, agentMaxBytes, maxAgentStringBytes)
	}
	p.MaxBytes = limit
	return nil
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
	// Meta is the x-agent siblings of the value at root, including when root
	// is nested (data.data). Their From paths stay relative to the body.
	parent := top
	prefix := ""
	segments := splitPath(root)
	target := top
	for i, key := range segments {
		if target.Type != "object" || target.Properties[key] == nil {
			return Result{}, fmt.Errorf("response has no %q", root)
		}
		if i == len(segments)-1 {
			parent = target
			break
		}
		if prefix != "" {
			prefix += "."
		}
		prefix += key
		if target, err = doc.resolve(target.Properties[key]); err != nil {
			return Result{}, err
		}
	}
	if len(segments) > 0 {
		if target, err = doc.resolve(parent.Properties[segments[len(segments)-1]]); err != nil {
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
	if out.Fields, err = doc.fields(target, "", out.List); err != nil {
		return Result{}, err
	}
	if len(out.Fields) == 0 {
		return Result{}, fmt.Errorf("response %q exposes no fields; tag them with %s", root, extAgent)
	}
	if root != "" {
		envelope := *parent
		envelope.Properties = map[string]*specSchema{}
		last := segments[len(segments)-1]
		for key, prop := range parent.Properties {
			if key != last {
				envelope.Properties[key] = prop
			}
		}
		metaPrefix := ""
		if prefix != "" {
			metaPrefix = prefix + "."
		}
		if out.Meta, err = doc.fields(&envelope, metaPrefix, false); err != nil {
			return Result{}, err
		}
	}
	return out, nil
}

// fields lists the projected fields of obj. Nested objects marked with
// x-agent are flattened: their marked fields are named on their own. A list of
// objects keeps each element's marked fields under the list's name. list
// drops fields marked x-agent-detail.
func (doc *specDocument) fields(obj *specSchema, prefix string, list bool) ([]Field, error) {
	var out []Field
	for _, key := range sortedKeys(obj.Properties) {
		raw := obj.Properties[key]
		name, exposed, err := agentName(raw.Agent, key)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", prefix+key, err)
		}
		if !exposed {
			if raw.AgentDetail {
				return nil, fmt.Errorf("field %q: %s needs %s", prefix+key, extAgentDetail, extAgent)
			}
			continue
		}
		if list && raw.AgentDetail {
			continue
		}
		from := prefix + key
		runes, err := fieldRunes(raw)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", from, err)
		}
		prop, err := doc.resolve(raw)
		if err != nil {
			return nil, err
		}
		switch {
		case isScalar(prop.Type):
			if runes > 0 && prop.Type != "string" {
				return nil, fmt.Errorf("field %q: %s applies to strings", from, extAgentMaxRunes)
			}
			out = append(out, Field{From: from, As: name, MaxRunes: runes})
		case prop.Type == "array":
			items, err := doc.resolve(prop.Items)
			if err != nil {
				return nil, err
			}
			if items != nil && len(items.Properties) > 0 {
				if runes > 0 {
					return nil, fmt.Errorf("field %q: %s applies to strings", from, extAgentMaxRunes)
				}
				elem, err := doc.fields(items, "", list)
				if err != nil {
					return nil, fmt.Errorf("field %q: %w", from, err)
				}
				if len(elem) == 0 {
					return nil, fmt.Errorf("field %q: list items expose no fields", from)
				}
				for _, f := range elem {
					if len(f.Items) > 0 {
						return nil, fmt.Errorf("field %q: list items cannot hold lists of objects", from)
					}
				}
				out = append(out, Field{From: from, As: name, Items: elem})
				continue
			}
			if items == nil || !isScalar(items.Type) {
				return nil, fmt.Errorf("field %q: only lists of scalars or of objects with %s fields are projected",
					from, extAgent)
			}
			if runes > 0 && items.Type != "string" {
				return nil, fmt.Errorf("field %q: %s applies to strings", from, extAgentMaxRunes)
			}
			out = append(out, Field{From: from, As: name, MaxRunes: runes})
		case len(prop.Properties) > 0:
			if runes > 0 {
				return nil, fmt.Errorf("field %q: %s applies to strings", from, extAgentMaxRunes)
			}
			if name != key {
				return nil, fmt.Errorf("field %q: objects are flattened, so name their fields instead", from)
			}
			nested, err := doc.fields(prop, from+".", list)
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
					if strings.Contains(sub, ".") {
						out = append(out, Field{From: from, MapKey: sub, As: sub})
					} else {
						out = append(out, Field{From: from + "." + sub, As: sub})
					}
				}
			}
		default:
			return nil, fmt.Errorf("field %q: free-form object needs %s", from, extAgentKeys)
		}
	}
	return out, nil
}

func fieldRunes(raw *specSchema) (int, error) {
	if raw == nil || raw.AgentMaxRunes == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw.AgentMaxRunes)
	if err != nil || n < 1 || n > maxAgentFieldRunes {
		return 0, fmt.Errorf("%s %q must be an integer from 1 to %d",
			extAgentMaxRunes, raw.AgentMaxRunes, maxAgentFieldRunes)
	}
	return n, nil
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
	out.AgentDetail = out.AgentDetail || base.AgentDetail
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
