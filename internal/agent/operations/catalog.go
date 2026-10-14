// Package operations is the catalog of WeKnora HTTP API operations an agent
// may run on behalf of the signed-in user.
//
// Every entry is declared here by hand: the catalog is the allow-list, and
// nothing outside it is reachable. An operation names a registered route, its
// risk, the parameters the model may supply and the slice of the response the
// model gets back. Execution goes through the real router (see Dispatcher), so
// authentication, RBAC guards, KB sharing and audit behave exactly as they do
// for the web UI; the catalog only narrows what is reachable.
package operations

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Risk classifies an operation by its effect on user data.
type Risk string

const (
	// RiskRead has no side effects.
	RiskRead Risk = "read"
	// RiskWrite changes state in a recoverable way (reparse, status change).
	RiskWrite Risk = "write"
	// RiskDestructive removes data or cannot be undone.
	RiskDestructive Risk = "destructive"
)

func (r Risk) valid() bool {
	return r == RiskRead || r == RiskWrite || r == RiskDestructive
}

// Mutates reports whether running the operation needs the user's approval.
func (r Risk) Mutates() bool { return r == RiskWrite || r == RiskDestructive }

// Location is where a parameter goes in the HTTP request.
type Location string

// Parameter locations.
const (
	InPath  Location = "path"
	InQuery Location = "query"
	InBody  Location = "body"
)

// ParamType is the JSON type the model must supply.
type ParamType string

// Parameter types.
const (
	TypeString     ParamType = "string"
	TypeInteger    ParamType = "integer"
	TypeBoolean    ParamType = "boolean"
	TypeStringList ParamType = "string_list"
)

// Param is one model-facing parameter.
//
// Names are chosen for the model, not copied from the route: identifiers use
// the canonical keys (knowledge_base_id, knowledge_id, issue_id, ...) that the
// model-context handle policy recognizes, so handles the model saw elsewhere
// (b1, d3, i2) decode to real IDs before the request is built.
type Param struct {
	Name string
	In   Location
	// Key is the wire name: the route segment, query key or body field.
	// Defaults to Name.
	Key         string
	Type        ParamType
	Required    bool
	Description string
	Enum        []string
	// Minimum and Maximum bound integers when Maximum > 0.
	Minimum int
	Maximum int
	// MaxItems bounds string lists; defaults to defaultMaxListItems.
	MaxItems int
}

func (p Param) wireKey() string {
	if p.Key != "" {
		return p.Key
	}
	return p.Name
}

// Field copies one value from the response into the model-visible result.
type Field struct {
	// From is a dotted path inside the source object.
	From string
	// As is the output key; defaults to the last segment of From. Identifiers
	// should be renamed to the canonical handle keys so the model-context
	// policy turns them into handles.
	As string
}

func (f Field) outputKey() string {
	if f.As != "" {
		return f.As
	}
	if i := strings.LastIndexByte(f.From, '.'); i >= 0 {
		return f.From[i+1:]
	}
	return f.From
}

// Result is the projection of a response body. Anything not listed is
// dropped, so new fields added to a handler never reach the model unless
// they are added here.
type Result struct {
	// Root is a dotted path to the object or array to project, relative to
	// the response body. Empty means the body itself.
	Root string
	// List projects each element of the array at Root with Fields.
	List bool
	// Fields are relative to the object at Root, or to each element.
	Fields []Field
	// Meta fields are relative to the response body (e.g. a total count).
	Meta []Field
	// MaxItems caps List results; defaults to defaultMaxResultItems.
	MaxItems int
}

// Operation is one catalog entry.
type Operation struct {
	// ID is the stable model-facing name, "<domain>.<verb>".
	ID          string
	Summary     string
	Description string
	Risk        Risk
	Method      string
	// Route is the full path as registered on the router, with :param and
	// *param segments.
	Route  string
	Params []Param
	Result Result
	// Target, for mutating operations, is a read operation that resolves
	// what the call acts on. It runs first, through the same authorization,
	// and its result labels the approval prompt. Its parameters must be a
	// subset of this operation's.
	Target *Target
}

// Target names the read operation that describes a mutation's subject.
type Target struct {
	Operation string
	// Label is the result field shown to the user (e.g. "title").
	Label string
}

// Param returns the parameter declared under name.
func (op Operation) Param(name string) (Param, bool) {
	for _, p := range op.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

const (
	defaultMaxListItems   = 100
	defaultMaxResultItems = 50
)

var (
	operationIDPattern = regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)
	paramNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	routeParamPattern  = regexp.MustCompile(`^[:*][A-Za-z_][A-Za-z0-9_]*$`)
)

// forbiddenRoutePrefixes are API areas that must never be reachable from an
// agent: credentials, sessions, identity, tenancy administration and the
// agent surfaces themselves. Catalog construction fails if an entry points
// into one, so a careless addition is caught by tests rather than review.
var forbiddenRoutePrefixes = []string{
	"/api/v1/auth",
	"/api/v1/tenants",
	"/api/v1/users",
	"/api/v1/me",
	"/api/v1/system",
	"/api/v1/initialization",
	"/api/v1/organizations",
	"/api/v1/api-keys",
	"/api/v1/models",
	"/api/v1/model-credentials",
	"/api/v1/mcp-services",
	"/api/v1/mcp-endpoints",
	"/api/v1/sandbox",
	"/api/v1/skills",
	"/api/v1/im",
	"/api/v1/embed",
	"/api/v1/agents",
	"/api/v1/agent-chat",
	"/api/v1/knowledge-chat",
	"/api/v1/sessions",
	"/api/v1/datasource",
	"/api/v1/web-search",
	"/api/v1/storage",
	"/api/v1/vector-stores",
	"/api/v1/evaluation",
}

var allowedMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// Catalog is an immutable, validated set of operations.
type Catalog struct {
	ops  []Operation
	byID map[string]int
}

// NewCatalog validates ops and returns a catalog sorted by ID.
func NewCatalog(ops ...Operation) (*Catalog, error) {
	c := &Catalog{ops: append([]Operation(nil), ops...), byID: make(map[string]int, len(ops))}
	sort.Slice(c.ops, func(i, j int) bool { return c.ops[i].ID < c.ops[j].ID })
	for i, op := range c.ops {
		if _, dup := c.byID[op.ID]; dup {
			return nil, fmt.Errorf("operation %q declared twice", op.ID)
		}
		if err := validateOperation(op); err != nil {
			return nil, fmt.Errorf("operation %q: %w", op.ID, err)
		}
		c.byID[op.ID] = i
	}
	for _, op := range c.ops {
		if err := c.validateTarget(op); err != nil {
			return nil, fmt.Errorf("operation %q: %w", op.ID, err)
		}
	}
	return c, nil
}

// Get returns the operation with id.
func (c *Catalog) Get(id string) (Operation, bool) {
	if c == nil {
		return Operation{}, false
	}
	i, ok := c.byID[id]
	if !ok {
		return Operation{}, false
	}
	return c.ops[i], true
}

// All returns every operation, sorted by ID.
func (c *Catalog) All() []Operation {
	if c == nil {
		return nil
	}
	return append([]Operation(nil), c.ops...)
}

// Find returns operations matching every whitespace-separated term of query
// (case-insensitive, against ID, summary and description), optionally limited
// to one domain. An empty query matches everything.
func (c *Catalog) Find(query, domain string) []Operation {
	if c == nil {
		return nil
	}
	terms := strings.Fields(strings.ToLower(query))
	domain = strings.ToLower(strings.TrimSpace(domain))
	var out []Operation
	for _, op := range c.ops {
		if domain != "" && op.Domain() != domain {
			continue
		}
		haystack := strings.ToLower(op.ID + " " + op.Summary + " " + op.Description)
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, op)
		}
	}
	return out
}

// Domains lists the distinct operation domains.
func (c *Catalog) Domains() []string {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, op := range c.ops {
		if d := op.Domain(); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// Domain is the part of the ID before the dot.
func (op Operation) Domain() string {
	domain, _, _ := strings.Cut(op.ID, ".")
	return domain
}

func validateOperation(op Operation) error {
	if !operationIDPattern.MatchString(op.ID) {
		return fmt.Errorf("id must look like domain.verb")
	}
	if strings.TrimSpace(op.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if !op.Risk.valid() {
		return fmt.Errorf("invalid risk %q", op.Risk)
	}
	if !allowedMethods[op.Method] {
		return fmt.Errorf("unsupported method %q", op.Method)
	}
	if op.Risk == RiskRead && op.Method != http.MethodGet {
		return fmt.Errorf("read operations must use GET")
	}
	if op.Risk != RiskRead && op.Method == http.MethodGet {
		return fmt.Errorf("GET operations must be read-only")
	}
	if op.Method == http.MethodDelete && op.Risk != RiskDestructive {
		return fmt.Errorf("DELETE operations must be destructive")
	}
	if op.Risk.Mutates() && op.Target == nil {
		return fmt.Errorf("mutating operations must declare a target")
	}
	routeParams, err := parseRoute(op.Route)
	if err != nil {
		return err
	}

	seenNames := map[string]bool{}
	seenWire := map[Location]map[string]bool{}
	pathParams := map[string]bool{}
	for _, p := range op.Params {
		if !paramNamePattern.MatchString(p.Name) {
			return fmt.Errorf("param %q: invalid name", p.Name)
		}
		if seenNames[p.Name] {
			return fmt.Errorf("param %q declared twice", p.Name)
		}
		seenNames[p.Name] = true
		if strings.TrimSpace(p.Description) == "" {
			return fmt.Errorf("param %q: description is required", p.Name)
		}
		switch p.Type {
		case TypeString, TypeInteger, TypeBoolean, TypeStringList:
		default:
			return fmt.Errorf("param %q: invalid type %q", p.Name, p.Type)
		}
		if len(p.Enum) > 0 && p.Type != TypeString && p.Type != TypeStringList {
			return fmt.Errorf("param %q: enum requires a string type", p.Name)
		}
		if p.Maximum > 0 && (p.Type != TypeInteger || p.Minimum > p.Maximum) {
			return fmt.Errorf("param %q: invalid integer bounds", p.Name)
		}
		switch p.In {
		case InPath:
			if p.Type != TypeString || !p.Required {
				return fmt.Errorf("path param %q must be a required string", p.Name)
			}
			if _, ok := routeParams[p.wireKey()]; !ok {
				return fmt.Errorf("path param %q has no route segment %q", p.Name, p.wireKey())
			}
			pathParams[p.wireKey()] = true
		case InQuery:
		case InBody:
			if op.Method == http.MethodGet || op.Method == http.MethodDelete {
				return fmt.Errorf("param %q: %s requests carry no body", p.Name, op.Method)
			}
		default:
			return fmt.Errorf("param %q: invalid location %q", p.Name, p.In)
		}
		if seenWire[p.In] == nil {
			seenWire[p.In] = map[string]bool{}
		}
		if seenWire[p.In][p.wireKey()] {
			return fmt.Errorf("param %q: %s key %q declared twice", p.Name, p.In, p.wireKey())
		}
		seenWire[p.In][p.wireKey()] = true
	}
	for name := range routeParams {
		if !pathParams[name] {
			return fmt.Errorf("route segment %q has no param", name)
		}
	}

	if op.Result.List && op.Result.Root == "" && len(op.Result.Meta) > 0 {
		return fmt.Errorf("a list result at the body root cannot have meta fields")
	}
	seenOut := map[string]bool{}
	for _, f := range append(append([]Field(nil), op.Result.Fields...), op.Result.Meta...) {
		if f.From == "" {
			return fmt.Errorf("result field with empty path")
		}
		key := f.outputKey()
		if seenOut[key] {
			return fmt.Errorf("result key %q declared twice", key)
		}
		seenOut[key] = true
	}
	return nil
}

func (c *Catalog) validateTarget(op Operation) error {
	if op.Target == nil {
		return nil
	}
	target, ok := c.Get(op.Target.Operation)
	if !ok {
		return fmt.Errorf("target %q is not in the catalog", op.Target.Operation)
	}
	if target.Risk != RiskRead {
		return fmt.Errorf("target %q must be a read operation", target.ID)
	}
	if target.Result.List {
		return fmt.Errorf("target %q must return a single object", target.ID)
	}
	for _, p := range target.Params {
		own, ok := op.Param(p.Name)
		if !ok || own.Type != p.Type {
			return fmt.Errorf("target param %q is not declared with the same type", p.Name)
		}
		if p.Required && !own.Required {
			return fmt.Errorf("target param %q is required by the target", p.Name)
		}
	}
	labelled := false
	for _, f := range target.Result.Fields {
		if f.outputKey() == op.Target.Label {
			labelled = true
		}
	}
	if !labelled {
		return fmt.Errorf("target label %q is not a result field of %q", op.Target.Label, target.ID)
	}
	return nil
}

// parseRoute checks that route is a clean absolute path under /api/v1 and
// outside the forbidden areas, returning its parameter segment names.
func parseRoute(route string) (map[string]bool, error) {
	if !strings.HasPrefix(route, "/api/v1/") {
		return nil, fmt.Errorf("route %q must be under /api/v1", route)
	}
	for _, prefix := range forbiddenRoutePrefixes {
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			return nil, fmt.Errorf("route %q is in a forbidden area", route)
		}
	}
	params := map[string]bool{}
	segments := strings.Split(strings.TrimPrefix(route, "/"), "/")
	for i, seg := range segments {
		if seg == "" {
			return nil, fmt.Errorf("route %q has an empty segment", route)
		}
		if seg[0] == ':' || seg[0] == '*' {
			if !routeParamPattern.MatchString(seg) {
				return nil, fmt.Errorf("route %q has an invalid parameter %q", route, seg)
			}
			if seg[0] == '*' && i != len(segments)-1 {
				return nil, fmt.Errorf("route %q: catch-all must be last", route)
			}
			params[seg[1:]] = true
			continue
		}
		for _, r := range seg {
			if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return nil, fmt.Errorf("route %q has an invalid segment %q", route, seg)
			}
		}
	}
	return params, nil
}
