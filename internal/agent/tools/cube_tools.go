package tools

// Cube semantic-layer tools (semantic modeling module): let the agent query
// business data under the current user's data-group permissions. The module
// is wired at startup via internal/semantic (CUBE_ENABLE); when disabled the
// constructors return nil and agent_service's registration loop skips them.
// Typical flow: call cube_meta first to learn the visible models and field
// names → assemble a cube_query to fetch data → use cube_sql to inspect the
// generated SQL when needed.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/semantic"
	"github.com/Tencent/WeKnora/internal/semantic/cubeclient"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Tool name constants (consumed by definitions.go / agent_service.go).
const (
	ToolCubeMeta  = "cube_meta"
	ToolCubeQuery = "cube_query"
	ToolCubeSQL   = "cube_sql"
)

// cubeIdentity extracts the caller identity the same way database_query does.
func cubeIdentity(ctx context.Context) (uint64, string, bool) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok || tenantID == 0 {
		return 0, "", false
	}
	userID, _ := ctx.Value(types.UserIDContextKey).(string)
	return tenantID, userID, true
}

// ---- cube_meta ----

const cubeMetaDescription = `List the semantic models (cube/view) visible to the current identity
in the semantic modeling module, with their fields.

## Usage
- You MUST call this tool before querying business data: the model and field
  names it returns (of the form "cube_name.field") are the valid inputs to
  cube_query
- Results are filtered by the current user's data permissions; models you
  cannot see are ones you have no access to, so do not guess field names
- Pass the cube parameter to filter by keyword and inspect a single model

## Output
Each model lists its title/description along with its measures, dimensions,
and segments; the title and description are the primary hints for
understanding what each field means.`

// CubeMetaInput is the input for the cube_meta tool.
type CubeMetaInput struct {
	Cube string `json:"cube" jsonschema:"optional, filter model name by keyword"`
}

// CubeMetaTool lists the semantic models visible to the caller.
type CubeMetaTool struct {
	BaseTool
	// boundModels, when non-empty, restricts the listed models to the bound set
	// (the scope the agent orchestration is limited to).
	boundModels []string
}

// NewCubeMetaTool returns nil when the semantic module is disabled.
// boundModels is the set of model names bound by the orchestration;
// nil/empty means no restriction (all visible models).
func NewCubeMetaTool(boundModels []string) *CubeMetaTool {
	if !semantic.Enabled() {
		return nil
	}
	return &CubeMetaTool{
		BaseTool:    NewBaseTool(ToolCubeMeta, cubeMetaDescription, utils.GenerateSchema[CubeMetaInput]()),
		boundModels: boundModels,
	}
}

// Execute lists permission-filtered models.
func (t *CubeMetaTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input CubeMetaInput
	_ = json.Unmarshal(args, &input)

	tenantID, userID, ok := cubeIdentity(ctx)
	if !ok {
		return &types.ToolResult{Success: false, Error: "missing user identity context"}, fmt.Errorf("missing identity")
	}
	meta, err := semantic.CubeMetaForUser(ctx, tenantID, userID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	if filtered := filterBoundCubes(meta.Cubes, t.boundModels); len(filtered) != len(meta.Cubes) {
		meta.Cubes = filtered
	}
	var b strings.Builder
	if len(meta.Cubes) == 0 {
		b.WriteString("The current identity has no visible semantic models. Either modeling has not\n")
		b.WriteString("started, or the current user has not been granted any data group.\n")
	}
	for _, cb := range meta.Cubes {
		if input.Cube != "" && !strings.Contains(strings.ToLower(cb.Name), strings.ToLower(input.Cube)) {
			continue
		}
		fmt.Fprintf(&b, "### %s (%s)\n", cb.Name, orDash(cb.Type))
		if cb.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", cb.Title)
		}
		if cb.Description != "" {
			fmt.Fprintf(&b, "description: %s\n", cb.Description)
		}
		writeMembers(&b, "measures", cb.Measures)
		writeMembers(&b, "dimensions", cb.Dimensions)
		writeMembers(&b, "segments", cb.Segments)
		b.WriteString("\n")
	}
	return &types.ToolResult{
		Success: true,
		Output:  b.String(),
		Data: map[string]interface{}{
			"models":       meta.Cubes,
			"display_type": "cube_meta",
		},
	}, nil
}

func writeMembers(b *strings.Builder, label string, members []cubeclient.MetaMember) {
	if len(members) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", label)
	for _, m := range members {
		desc := m.Title
		if m.Description != "" {
			desc = m.Title + " — " + m.Description
		}
		if m.Type != "" {
			fmt.Fprintf(b, "  - %s (%s): %s\n", m.Name, m.Type, desc)
		} else {
			fmt.Fprintf(b, "  - %s: %s\n", m.Name, desc)
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "cube"
	}
	return s
}

// ---- cube_query / cube_sql ----

const cubeQueryDescription = `Query business data via the Cube semantic layer (read-only).

## Usage
1. Call cube_meta first to confirm the visible models and field names
2. Assemble the query: measures (required, e.g. sales.amount), dimensions
   (optional, grouping), time_dimensions (time range/granularity), filters,
   order (sort), limit (default 200)
3. Field names must be fully qualified as "model_name.field_name"; do not
   invent field names

## Examples
{"measures": ["orders.count"], "dimensions": ["orders.status"],
 "time_dimensions": [{"dimension": "orders.created_at",
  "date_range": ["2025-01-01", "2025-12-31"], "granularity": "month"}]}
{"measures": ["stock.quantity"], "filters": [{"member": "stock.warehouse",
 "operator": "equals", "values": ["guangzhou_warehouse"]}]}

## Notes
- Results are filtered by the user's data permissions; if you get a
  "permission denied" hint, switch to a visible model instead of retrying
- results are in YAML row format; row count is capped — for finer detail
  narrow the time range or add more filters`

const cubeSQLDescription = `Show the SQL a cube_query would generate (syntax expansion only, not executed).

## Usage
The inputs are identical to cube_query; use this to explain the data
semantics to the user, verify query intent, or debug unexpected results.
To actually run the query, use cube_query instead.`

// CubeQueryInput is the input for the cube_query and cube_sql tools.
type CubeQueryInput struct {
	Measures   []string `json:"measures" jsonschema:"measure list, fully qualified e.g. sales.amount"`
	Dimensions []string `json:"dimensions" jsonschema:"dimension list, fully qualified e.g. orders.status"`
	// The fields below are all optional; omitempty also keeps the schema
	// generator from marking them as required
	TimeDimensions []CubeTimeDimension    `json:"time_dimensions,omitempty" jsonschema:"time dimension list"`
	Filters        []CubeQueryFilterInput `json:"filters,omitempty" jsonschema:"filter condition list"`
	Order          map[string]string      `json:"order,omitempty" jsonschema:"sort order, e.g. orders.created_at desc"`
	Limit          int                    `json:"limit,omitempty" jsonschema:"row cap, default 200"`
	Offset         int                    `json:"offset,omitempty" jsonschema:"offset"`
	Timezone       string                 `json:"timezone,omitempty" jsonschema:"timezone, e.g. Asia/Shanghai"`
}

// CubeTimeDimension is a time-based dimension for filtering and grouping.
type CubeTimeDimension struct {
	Dimension   string   `json:"dimension" jsonschema:"fully qualified time dimension"`
	DateRange   []string `json:"date_range" jsonschema:"date range [from, to]"`
	Granularity string   `json:"granularity" jsonschema:"granularity: day/week/month/quarter/year"`
}

// CubeQueryFilterInput is a single filter condition for cube_query.
type CubeQueryFilterInput struct {
	Member   string   `json:"member" jsonschema:"fully qualified filter field"`
	Operator string   `json:"operator" jsonschema:"operator, e.g. equals/contains/gt/inRange"`
	Values   []string `json:"values" jsonschema:"value list"`
}

func (in *CubeQueryInput) toPreviewQuery() *semantic.PreviewQuery {
	convert := func(fs []CubeQueryFilterInput) []semantic.QueryFilter {
		out := make([]semantic.QueryFilter, 0, len(fs))
		for _, f := range fs {
			out = append(out, semantic.QueryFilter{
				Member: f.Member, Operator: f.Operator, Values: f.Values,
			})
		}
		return out
	}
	tds := make([]semantic.TimeDimension, 0, len(in.TimeDimensions))
	for _, td := range in.TimeDimensions {
		tds = append(tds, semantic.TimeDimension{
			Dimension: td.Dimension, DateRange: td.DateRange, Granularity: td.Granularity,
		})
	}
	return &semantic.PreviewQuery{
		Measures: in.Measures, Dimensions: in.Dimensions,
		TimeDimensions: tds, Filters: convert(in.Filters),
		Order: in.Order, Limit: in.Limit, Offset: in.Offset, Timezone: in.Timezone,
	}
}

// CubeQueryTool runs a Cube query under the caller's data-group permissions.
type CubeQueryTool struct {
	BaseTool
	boundModels []string
}

// NewCubeQueryTool returns nil when the semantic module is disabled.
func NewCubeQueryTool(boundModels []string) *CubeQueryTool {
	if !semantic.Enabled() {
		return nil
	}
	return &CubeQueryTool{
		BaseTool:    NewBaseTool(ToolCubeQuery, cubeQueryDescription, utils.GenerateSchema[CubeQueryInput]()),
		boundModels: boundModels,
	}
}

// Execute runs the query under the caller's identity.
func (t *CubeQueryTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][CubeQuery] Execute started")
	input, toolErr := parseCubeQueryArgs(args)
	if toolErr != nil {
		return &types.ToolResult{Success: false, Error: toolErr.Error()}, toolErr
	}
	tenantID, userID, ok := cubeIdentity(ctx)
	if !ok {
		return &types.ToolResult{Success: false, Error: "missing user identity context"}, fmt.Errorf("missing identity")
	}
	if err := validateBoundModels(t.boundModels, input); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	resp, err := semantic.CubeQueryForUser(ctx, tenantID, userID, input.toPreviewQuery())
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	output, rowCount := formatCubeRows(resp.Data)
	// empty result on members the identity cannot see: make the denial explicit
	meta, _ := semantic.CubeMetaForUser(ctx, tenantID, userID)
	if hint := semantic.WarnDenied(meta, input.toPreviewQuery()); hint != "" {
		output = hint + "\n\n" + output
	}
	logger.Infof(ctx, "[Tool][CubeQuery] returned %d rows", rowCount)
	sqlNote := ""
	if len(resp.GeneratedSQL) > 0 {
		sqlNote = "\n\nGenerated SQL (for reference):\n" + strings.Join(resp.GeneratedSQL, "\n")
	}
	return &types.ToolResult{
		Success: true,
		Output:  output + sqlNote,
		Data: map[string]interface{}{
			"rows":         resp.Data,
			"row_count":    rowCount,
			"sql":          resp.GeneratedSQL,
			"display_type": "cube_query",
		},
	}, nil
}

// CubeSQLTool dry-runs a Cube query and returns the generated SQL.
type CubeSQLTool struct {
	BaseTool
	boundModels []string
}

// NewCubeSQLTool returns nil when the semantic module is disabled.
func NewCubeSQLTool(boundModels []string) *CubeSQLTool {
	if !semantic.Enabled() {
		return nil
	}
	return &CubeSQLTool{
		BaseTool:    NewBaseTool(ToolCubeSQL, cubeSQLDescription, utils.GenerateSchema[CubeQueryInput]()),
		boundModels: boundModels,
	}
}

// Execute dry-runs the query and returns the generated SQL.
func (t *CubeSQLTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	input, toolErr := parseCubeQueryArgs(args)
	if toolErr != nil {
		return &types.ToolResult{Success: false, Error: toolErr.Error()}, toolErr
	}
	tenantID, userID, ok := cubeIdentity(ctx)
	if !ok {
		return &types.ToolResult{Success: false, Error: "missing user identity context"}, fmt.Errorf("missing identity")
	}
	if err := validateBoundModels(t.boundModels, input); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	resp, err := semantic.CubeSQLForUser(ctx, tenantID, userID, input.toPreviewQuery())
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	var b strings.Builder
	b.WriteString("Generated SQL (not executed):\n\n```sql\n")
	for _, s := range resp.SQL {
		b.WriteString(strings.TrimSpace(s) + ";\n")
	}
	b.WriteString("```\n")
	return &types.ToolResult{
		Success: true,
		Output:  b.String(),
		Data: map[string]interface{}{
			"sql":          resp.SQL,
			"display_type": "cube_sql",
		},
	}, nil
}

// filterBoundCubes filters meta entries by the orchestration-bound model names
// (empty bound = no filtering).
func filterBoundCubes(cubes []cubeclient.MetaCube, bound []string) []cubeclient.MetaCube {
	if len(bound) == 0 {
		return cubes
	}
	allowed := make(map[string]bool, len(bound))
	for _, n := range bound {
		allowed[n] = true
	}
	out := make([]cubeclient.MetaCube, 0, len(cubes))
	for _, c := range cubes {
		if allowed[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// validateBoundModels rejects model members outside the orchestration scope.
func validateBoundModels(bound []string, input *CubeQueryInput) error {
	if len(bound) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(bound))
	for _, n := range bound {
		allowed[n] = true
	}
	check := func(member string) error {
		if i := strings.Index(member, "."); i > 0 {
			if !allowed[member[:i]] {
				joined := strings.Join(bound, ", ")
				return fmt.Errorf("model %s is not bound to the current agent; available models: %s",
					member[:i], joined)
			}
		}
		return nil
	}
	for _, m := range input.Measures {
		if err := check(m); err != nil {
			return err
		}
	}
	for _, m := range input.Dimensions {
		if err := check(m); err != nil {
			return err
		}
	}
	// Also validate filters, time_dimensions, and order keys.
	for _, f := range input.Filters {
		if err := check(f.Member); err != nil {
			return err
		}
	}
	for _, td := range input.TimeDimensions {
		if err := check(td.Dimension); err != nil {
			return err
		}
	}
	for key := range input.Order {
		if err := check(key); err != nil {
			return err
		}
	}
	return nil
}

// parseCubeQueryArgs tolerates absent input and the WeKnora pipeline's
// stringified array/object args (a client quirk known since the cube-mcp era).
func parseCubeQueryArgs(args json.RawMessage) (*CubeQueryInput, error) {
	in := &CubeQueryInput{}
	if len(args) > 0 {
		// lenient pass: when an array/object field value is serialized as a
		// string, unwrap it before binding
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(args, &raw); err == nil {
			for _, k := range []string{"measures", "dimensions", "time_dimensions", "filters", "order"} {
				v, ok := raw[k]
				if !ok {
					continue
				}
				var str string
				if json.Unmarshal(v, &str) == nil && str != "" {
					var decoded any
					if json.Unmarshal([]byte(str), &decoded) == nil {
						if nv, err := json.Marshal(decoded); err == nil {
							raw[k] = nv
						}
					}
				}
			}
			if fixed, err := json.Marshal(raw); err == nil {
				args = fixed
			}
		}
		if err := json.Unmarshal(args, in); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	}
	if len(in.Measures) == 0 && len(in.Dimensions) == 0 {
		return nil, fmt.Errorf("at least one of measures or dimensions is required")
	}
	return in, nil
}

// formatCubeRows renders rows as compact YAML-ish text.
func formatCubeRows(rows []map[string]interface{}) (string, int) {
	if len(rows) == 0 {
		return "(no data)", 0
	}
	var b strings.Builder
	for _, row := range rows {
		b.WriteString("- ")
		first := true
		for k, v := range row {
			if !first {
				b.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&b, "%s: %v", k, v)
		}
		b.WriteString("\n")
	}
	return b.String(), len(rows)
}
