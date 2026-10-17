// Package operations is the catalog of WeKnora HTTP API operations an agent
// may run on behalf of the signed-in user.
//
// The catalog is the allow-list: nothing outside it is reachable. Entries are
// declared with x-agent annotations on the handlers and compiled from the
// Swagger document (see FromSpec). An operation names a registered route, its
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

	"github.com/Tencent/WeKnora/internal/types"
)

// Risk classifies an operation by its effect on user data.
type Risk string

const (
	// RiskRead has no side effects.
	RiskRead Risk = "read"
	// RiskProbe changes no data but does real work to answer: it connects to
	// an external service or runs a retrieval. It runs without approval,
	// within a per-run budget, and uses POST because the API does.
	RiskProbe Risk = "probe"
	// RiskWrite changes state in a recoverable way (reparse, status change).
	RiskWrite Risk = "write"
	// RiskDestructive removes data or cannot be undone.
	RiskDestructive Risk = "destructive"
)

func (r Risk) valid() bool {
	return r == RiskRead || r == RiskProbe || r == RiskWrite || r == RiskDestructive
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
	TypeString      ParamType = "string"
	TypeInteger     ParamType = "integer"
	TypeNumber      ParamType = "number"
	TypeBoolean     ParamType = "boolean"
	TypeStringList  ParamType = "string_list"
	TypeIntegerList ParamType = "integer_list"
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
	Key      string
	Type     ParamType
	Required bool
	// AllowEmpty separates an agent-required field from an API nonempty constraint.
	AllowEmpty  bool
	Description string
	Enum        []string
	// Minimum and Maximum bound integers when Maximum > 0.
	Minimum int
	Maximum int
	// MaxItems bounds lists; defaults to defaultMaxListItems.
	MaxItems int
	// MaxBytes overrides maxStringParamBytes for this string. Zero keeps the
	// default. It cannot exceed maxAgentStringBytes.
	MaxBytes int
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
	// MapKey selects one literal key within From, including keys containing dots.
	// Used for explicitly allow-listed keys of free-form maps.
	MapKey string
	// As is the output key; defaults to the last segment of From. Identifiers
	// should be renamed to the canonical handle keys so the model-context
	// policy turns them into handles.
	As string
	// MaxRunes overrides the projected string length for this field. Zero
	// keeps maxProjectedStringRunes.
	MaxRunes int
	// Items projects each element of a list of objects. Element fields are
	// relative to the element and cannot declare Items themselves.
	Items []Field
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
	// LabelParam, for a creation with nothing existing to act on, names the
	// required string parameter (the new name) that labels the approval in
	// place of a Target.
	LabelParam string
	// Role is the lowest workspace role the route's guards let through;
	// empty takes the default for Risk. It only decides what a caller is
	// offered: the router still authorizes every call, and a router test
	// keeps the two in step.
	Role types.TenantRole
	// SystemAdmin requires the independent platform administrator permission.
	SystemAdmin bool
	// AlwaysConfirm makes a write ask every time, whatever the access level
	// and session grants: installing a skill runs fetched code as root in
	// the image every later session boots, so it is never pre-approved.
	AlwaysConfirm bool
}

// defaultRole is the role the route guards usually ask of an operation:
// reading or probing needs a viewer, changing content a contributor.
func defaultRole(risk Risk) types.TenantRole {
	if !risk.Mutates() {
		return types.TenantRoleViewer
	}
	return types.TenantRoleContributor
}

// Access is what one run may do with the catalog.
type Access struct {
	// Role is the caller's workspace role. It filters only when EnforceRole
	// is set, matching the router, which skips role checks otherwise.
	Role        types.TenantRole
	EnforceRole bool
	SystemAdmin bool
	// Level is the user's assistant access level.
	Level types.AssistantAccessLevel
}

// NeedsApproval reports whether running op must wait for the user.
func (a Access) NeedsApproval(op Operation) bool {
	return op.AlwaysConfirm || a.NeedsApprovalFor(op.Risk)
}

// NeedsApprovalFor reports whether a change of the given risk must wait for
// the user; it also covers tools outside the catalog.
func (a Access) NeedsApprovalFor(risk Risk) bool {
	switch risk {
	case RiskRead, RiskProbe:
		return false
	case RiskWrite:
		return a.Level != types.AssistantAccessAutoWrite
	default:
		return true
	}
}

// Withholding says why an access leaves an operation out.
type Withholding struct {
	// ReadOnly: the user set the assistant to read-only.
	ReadOnly    bool
	SystemAdmin bool
	// Role is the workspace role the operation needs when the caller's falls
	// short.
	Role types.TenantRole
}

// Withheld reports why a leaves op out of c, if it does. A mutation whose
// approval target is withheld is withheld for the same reason, so every
// offered mutation can still show the user what it acts on.
func (c *Catalog) Withheld(a Access, op Operation) (Withholding, bool) {
	if a.Level == types.AssistantAccessReadOnly && op.Risk.Mutates() {
		return Withholding{ReadOnly: true}, true
	}
	if op.SystemAdmin && !a.SystemAdmin {
		return Withholding{SystemAdmin: true}, true
	}
	if !op.SystemAdmin && a.EnforceRole && !a.Role.HasPermission(op.Role) {
		return Withholding{Role: op.Role}, true
	}
	if t := op.Target; t != nil {
		targets := []string{t.Operation}
		if t.Items != nil {
			targets = append(targets, t.Items.Operation)
		}
		for _, id := range targets {
			target, ok := c.Get(id)
			if !ok {
				return Withholding{}, true
			}
			if w, withheld := c.Withheld(a, target); withheld {
				return w, true
			}
		}
	}
	return Withholding{}, false
}

// For returns the operations a allows; see Withheld.
func (c *Catalog) For(a Access) *Catalog {
	if c == nil {
		return nil
	}
	out := &Catalog{byID: map[string]int{}}
	for _, op := range c.ops {
		if _, withheld := c.Withheld(a, op); withheld {
			continue
		}
		out.byID[op.ID] = len(out.ops)
		out.ops = append(out.ops, op)
	}
	return out
}

// Target names the read operation that describes a mutation's subject.
type Target struct {
	Operation string
	// Label is the result field shown to the user (e.g. "title").
	Label string
	// Items, for a batch mutation, also resolves every item it acts on.
	Items *ItemTarget
	// RequireComplete blocks a full replacement when the target read loses data.
	RequireComplete bool
}

// ItemTarget runs a read once per item of a list parameter, so the approval
// names each item rather than showing IDs.
type ItemTarget struct {
	// Param is the mutation's list parameter; at most MaxApprovalItems long.
	Param     string
	Operation string
	// As is the target parameter that receives each item.
	As    string
	Label string
}

// MaxApprovalItems bounds a batch whose items the approval resolves, so the
// user sees every item they approve.
const MaxApprovalItems = 20

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
	// maxDeclaredResultItems is the largest max_items an operation may
	// declare. Unpaginated lists raise their cap explicitly; anything larger
	// belongs behind a page parameter.
	maxDeclaredResultItems = 500
)

var (
	operationIDPattern = regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)
	paramNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	routeParamPattern  = regexp.MustCompile(`^[:*][A-Za-z_][A-Za-z0-9_]*$`)
)

// allowedRouteAreas are the first path segments under /api/v1 an operation
// may live in: the user's knowledge content, their agents, the models and
// MCP services an agent can be bound to, and their own conversations. Every
// other area is refused,
// so an API added later (credentials, integrations, identity, the agent's
// own approval and prompt endpoints) stays unreachable until it is listed
// here on purpose. Catalog construction fails otherwise, so a careless
// annotation is caught by tests rather than review.
var allowedRouteAreas = map[string]bool{
	"knowledge-bases":        true,
	"shared-knowledge-bases": true,
	"knowledge":              true,
	"knowledgebase":          true, // Wiki
	"chunks":                 true,
	"faq":                    true,
	"agents":                 true,
	"skills":                 true,
	// Model and MCP responses carry endpoints and headers next to the
	// names; only fields tagged x-agent are projected, so annotate the
	// identifying ones and nothing else.
	"models":               true,
	"mcp-services":         true,
	"web-search-providers": true,
	// Saved data sources: status, connection tests, sync settings and control.
	"datasource": true,
	// Sandbox configs hold provider credentials; see sandboxSkillsRoute.
	"sandbox-configs": true,
	// The caller's own conversations, read-only; see readOnlyRouteAreas.
	"sessions": true,
	"messages": true,
}

// readOnlyRouteAreas admit only operations that change nothing. Deleting,
// stopping, rewinding or steering a conversation is the user's to do in the
// chat itself, and content the assistant reads (documents, past answers)
// must never be able to talk it into erasing history.
var readOnlyRouteAreas = map[string]bool{
	"sessions": true,
	"messages": true,
	"system":   true, // Only info and parser-engines are reachable; see parseRoute.
}

// forbiddenRouteSegments are refused anywhere inside an allowed area:
// sharing and publishing change who can reach a knowledge base or agent,
// credentials never pass through the model, and a skill install's installer
// conversation (its guidance, transcript and event stream) belongs to the
// user watching it; the user handles these themselves.
var forbiddenRouteSegments = map[string]bool{
	"shares":               true,
	"embed-channels":       true,
	"im-channels":          true,
	"credentials":          true,
	"validate-credentials": true,
	"oauth":                true,
	"guidance":             true,
	"transcript":           true,
	"install-events":       true,
}

// sandboxSkillsRoute reports whether a route in the sandbox-configs area
// stays within one config's installed skills. The config itself, its
// sandboxes and the workspace policy are never reachable.
func sandboxSkillsRoute(rest string) bool {
	segments := strings.Split(rest, "/")
	return len(segments) >= 3 && segments[0] == "sandbox-configs" &&
		strings.HasPrefix(segments[1], ":") && segments[2] == "skills"
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
	for i := range c.ops {
		if c.ops[i].Role == "" {
			c.ops[i].Role = defaultRole(c.ops[i].Risk)
		}
	}
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
	if op.Risk == RiskProbe && op.Method != http.MethodPost {
		return fmt.Errorf("probe operations must use POST")
	}
	if op.Risk == RiskProbe && (op.Target != nil || op.LabelParam != "") {
		return fmt.Errorf("probe operations change nothing, so they take no approval target")
	}
	if op.Risk != RiskRead && op.Method == http.MethodGet {
		return fmt.Errorf("GET operations must be read-only")
	}
	if op.Method == http.MethodDelete && op.Risk != RiskDestructive {
		return fmt.Errorf("DELETE operations must be destructive")
	}
	if op.Risk.Mutates() && op.Target == nil && op.LabelParam == "" {
		return fmt.Errorf("mutating operations must declare a target")
	}
	if op.LabelParam != "" {
		if op.Target != nil || op.Risk != RiskWrite || op.Method != http.MethodPost {
			return fmt.Errorf("label_param is for a POST write without a target")
		}
		p, ok := op.Param(op.LabelParam)
		if !ok || p.Type != TypeString || !p.Required {
			return fmt.Errorf("label_param %q must be a required string param", op.LabelParam)
		}
	}
	if op.AlwaysConfirm && op.Risk != RiskWrite {
		return fmt.Errorf("always_confirm is for writes; destructive operations always ask")
	}
	if !op.Role.IsValid() {
		return fmt.Errorf("invalid role %q", op.Role)
	}
	routeParams, area, err := parseRoute(op.Route)
	if err != nil {
		return err
	}
	if systemAdminReadRoute(strings.TrimPrefix(op.Route, "/api/v1/")) != op.SystemAdmin {
		return fmt.Errorf("route %q requires matching system_admin declaration", op.Route)
	}
	if op.SystemAdmin && (op.Risk != RiskRead || op.Method != http.MethodGet) {
		return fmt.Errorf("system administrator operations must be read-only GET requests")
	}
	if readOnlyRouteAreas[area] && op.Risk.Mutates() {
		return fmt.Errorf("route %q is in an area an agent may only read", op.Route)
	}
	if area == "tenants" && op.Method != http.MethodGet &&
		(op.Route != "/api/v1/tenants/current" || op.Method != http.MethodPatch || op.Risk != RiskWrite) {
		return fmt.Errorf("route %q only permits current workspace reads and profile patches", op.Route)
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
		case TypeString, TypeInteger, TypeNumber, TypeBoolean, TypeStringList, TypeIntegerList:
		default:
			return fmt.Errorf("param %q: invalid type %q", p.Name, p.Type)
		}
		if len(p.Enum) > 0 && p.Type != TypeString && p.Type != TypeStringList {
			return fmt.Errorf("param %q: enum requires a string type", p.Name)
		}
		if p.Maximum > 0 && (p.Type != TypeInteger || p.Minimum > p.Maximum) {
			return fmt.Errorf("param %q: invalid integer bounds", p.Name)
		}
		if p.MaxBytes != 0 && (p.Type != TypeString && p.Type != TypeStringList ||
			p.MaxBytes < 1 || p.MaxBytes > maxAgentStringBytes) {
			return fmt.Errorf("param %q: invalid string length", p.Name)
		}
		switch p.In {
		case InPath:
			if (p.Type != TypeString && p.Type != TypeInteger) || !p.Required {
				return fmt.Errorf("path param %q must be a required string or integer", p.Name)
			}
			if p.Type == TypeInteger && strings.HasSuffix(op.Route, "/*"+p.wireKey()) {
				return fmt.Errorf("path param %q: a catch-all must be a string", p.Name)
			}
			if _, ok := routeParams[p.wireKey()]; !ok {
				return fmt.Errorf("path param %q has no route segment %q", p.Name, p.wireKey())
			}
			pathParams[p.wireKey()] = true
		case InQuery:
		case InBody:
			if op.Method == http.MethodGet {
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

	if op.Result.MaxItems < 0 || op.Result.MaxItems > maxDeclaredResultItems {
		return fmt.Errorf("max_items must be between 0 and %d", maxDeclaredResultItems)
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
		if err := validateItemFields(key, f.Items); err != nil {
			return err
		}
	}
	return nil
}

func validateItemFields(parent string, items []Field) error {
	seen := map[string]bool{}
	for _, f := range items {
		if f.From == "" {
			return fmt.Errorf("result field %q has an item with empty path", parent)
		}
		if len(f.Items) > 0 {
			return fmt.Errorf("result field %q: items cannot hold lists of objects", parent)
		}
		key := f.outputKey()
		if seen[key] {
			return fmt.Errorf("result field %q: item key %q declared twice", parent, key)
		}
		seen[key] = true
	}
	return nil
}

func (c *Catalog) validateTarget(op Operation) error {
	if op.Target == nil {
		return nil
	}
	if err := c.validateRead(op, op.Target.Operation, op.Target.Label, "", ""); err != nil {
		return err
	}
	items := op.Target.Items
	if items == nil {
		return nil
	}
	list, ok := op.Param(items.Param)
	if !ok || !list.Required || (list.Type != TypeStringList && list.Type != TypeIntegerList) {
		return fmt.Errorf("target items %q must be a required list", items.Param)
	}
	if list.MaxItems <= 0 || list.MaxItems > MaxApprovalItems {
		return fmt.Errorf("target items %q must be capped at %d or fewer", items.Param, MaxApprovalItems)
	}
	return c.validateRead(op, items.Operation, items.Label, items.As, list.itemType())
}

// itemType is the type of one element of a list param.
func (p Param) itemType() ParamType {
	if p.Type == TypeIntegerList {
		return TypeInteger
	}
	return TypeString
}

// validateRead checks a target read of op: a single-object read whose
// parameters op supplies (item, if set, is filled per item instead, with
// itemType) and whose result has label.
func (c *Catalog) validateRead(op Operation, id, label, item string, itemType ParamType) error {
	target, ok := c.Get(id)
	if !ok {
		return fmt.Errorf("target %q is not in the catalog", id)
	}
	if target.Risk != RiskRead {
		return fmt.Errorf("target %q must be a read operation", target.ID)
	}
	if target.Result.List {
		return fmt.Errorf("target %q must return a single object", target.ID)
	}
	if item != "" {
		p, ok := target.Param(item)
		if !ok || p.Type != itemType || !p.Required {
			return fmt.Errorf("target %q has no required %s param %q for each item", target.ID, itemType, item)
		}
		if _, clash := op.Param(item); clash {
			return fmt.Errorf("target item param %q is also a param of %q", item, op.ID)
		}
	}
	for _, p := range target.Params {
		if p.Name == item {
			continue
		}
		own, ok := op.Param(p.Name)
		if !ok || own.Type != p.Type {
			return fmt.Errorf("target param %q is not declared with the same type", p.Name)
		}
		if p.Required && !own.Required {
			return fmt.Errorf("target param %q is required by the target", p.Name)
		}
	}
	for _, f := range target.Result.Fields {
		if f.outputKey() == label {
			return nil
		}
	}
	return fmt.Errorf("target label %q is not a result field of %q", label, target.ID)
}

// systemAdminReadRoute is deliberately exact: no other platform routes are exposed.
func systemAdminReadRoute(rest string) bool {
	switch rest {
	case "system/admin/list", "system/admin/users", "system/admin/settings-summary",
		"system/admin/runtime/queues", "system/admin/runtime/queues/:queue/tasks":
		return true
	default:
		return false
	}
}

// parseRoute checks that route is a clean absolute path in an allowed area
// under /api/v1, returning its parameter segment names and its area.
func parseRoute(route string) (map[string]bool, string, error) {
	const base = "/api/v1/"
	if !strings.HasPrefix(route, base) {
		return nil, "", fmt.Errorf("route %q must be under /api/v1", route)
	}
	rest := strings.TrimPrefix(route, base)
	area, _, _ := strings.Cut(rest, "/")
	currentWorkspace := rest == "tenants/current" || rest == "tenants/current/members"
	allowed := allowedRouteAreas[area] || rest == "system/parser-engines" || rest == "system/info" ||
		currentWorkspace || systemAdminReadRoute(rest)
	if !allowed || (area == "sandbox-configs" && !sandboxSkillsRoute(rest)) {
		return nil, "", fmt.Errorf("route %q is outside the areas an agent may reach", route)
	}
	params := map[string]bool{}
	segments := strings.Split(strings.TrimPrefix(route, "/"), "/")
	for i, seg := range segments {
		if seg == "" {
			return nil, "", fmt.Errorf("route %q has an empty segment", route)
		}
		if forbiddenRouteSegments[seg] {
			return nil, "", fmt.Errorf("route %q reaches %q, which an agent may not", route, seg)
		}
		if seg[0] == ':' || seg[0] == '*' {
			if !routeParamPattern.MatchString(seg) {
				return nil, "", fmt.Errorf("route %q has an invalid parameter %q", route, seg)
			}
			if seg[0] == '*' && i != len(segments)-1 {
				return nil, "", fmt.Errorf("route %q: catch-all must be last", route)
			}
			params[seg[1:]] = true
			continue
		}
		for _, r := range seg {
			if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return nil, "", fmt.Errorf("route %q has an invalid segment %q", route, seg)
			}
		}
	}
	return params, area, nil
}
