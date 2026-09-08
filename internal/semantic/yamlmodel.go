package semantic

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Tencent/WeKnora/internal/semantic/dbinspector"
)

// Cube YAML data-model layer. The YAML text is the canonical model
// definition (stored in SemanticModel.DraftYAML / PublishedYAML); the
// studio form is a structured view over it. Inline Extra maps keep any
// field the form does not model (pre_aggregations, format, meta, ...) so
// form round-trips never drop hand-written YAML.

// Slug regex shared by connection / model / group names.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidSlug reports whether name is a valid slug.
func ValidSlug(name string) bool { return slugPattern.MatchString(name) }

// ModelDoc is one published YAML file: exactly one cube or one view.
type ModelDoc struct {
	Cubes []CubeDef `yaml:"cubes,omitempty"`
	Views []ViewDef `yaml:"views,omitempty"`
}

// CubeDef mirrors the Cube YAML cube schema (camelCase JS keys map to
// snake_case in YAML, e.g. dataSource → data_source, primaryKey → primary_key).
type CubeDef struct {
	Name            string                   `yaml:"name"`
	Title           string                   `yaml:"title,omitempty"`
	Description     string                   `yaml:"description,omitempty"`
	SQL             string                   `yaml:"sql,omitempty"`
	DataSource      string                   `yaml:"data_source,omitempty"`
	Joins           []JoinDef                `yaml:"joins,omitempty"`
	Measures        []MemberDef              `yaml:"measures,omitempty"`
	Dimensions      []MemberDef              `yaml:"dimensions,omitempty"`
	Segments        []MemberDef              `yaml:"segments,omitempty"`
	AccessPolicy    []PolicyRule             `yaml:"access_policy,omitempty"`
	PreAggregations []map[string]interface{} `yaml:"pre_aggregations,omitempty"`
	// Extra preserves unmodelled cube-level keys on round-trip.
	Extra map[string]interface{} `yaml:",inline"`
}

// ViewDef intentionally stays loose: views aggregate members across cubes
// and their member syntax is expressive enough that v1 authors them in the
// YAML source mode; only the identity fields are parsed for listing.
type ViewDef struct {
	Name        string      `yaml:"name"`
	Title       string      `yaml:"title,omitempty"`
	Description string      `yaml:"description,omitempty"`
	Extra       interface{} `yaml:",inline"`
}

// JoinDef is one cube join.
type JoinDef struct {
	Name         string                 `yaml:"name"`
	SQL          string                 `yaml:"sql"`
	Relationship string                 `yaml:"relationship,omitempty"`
	Extra        map[string]interface{} `yaml:",inline"`
}

// MemberDef is one measure/dimension/segment.
type MemberDef struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type,omitempty"`
	SQL         string `yaml:"sql,omitempty"`
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
	PrimaryKey  bool   `yaml:"primary_key,omitempty"`
	// Extra preserves member-level keys the form does not model (format,
	// shown, granularities, ...).
	Extra map[string]interface{} `yaml:",inline"`
}

// PolicyRule is one accessPolicy rule. We always emit a default-deny rule
// for group "*", then one allow rule per permitted group (admin is granted
// implicitly by every model for operator access).
type PolicyRule struct {
	Group       string                 `yaml:"group"`
	MemberLevel *MemberLevel           `yaml:"member_level,omitempty"`
	Extra       map[string]interface{} `yaml:",inline"`
}

// MemberLevel mirrors Cube's memberLevel { includes } shape; Includes is
// either "*" (all members) or a list.
type MemberLevel struct {
	Includes interface{} `yaml:"includes,omitempty"`
}

// ParseModelYAML decodes a published/draft YAML document.
func ParseModelYAML(text string) (*ModelDoc, error) {
	var doc ModelDoc
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("YAML parse failed: %w", err)
	}
	nCubes := len(doc.Cubes)
	nViews := len(doc.Views)
	if nCubes+nViews == 0 {
		return nil, fmt.Errorf("YAML defines no cube or view")
	}
	if nCubes+nViews > 1 {
		return nil, fmt.Errorf("a model file may define only one cube or view")
	}
	return &doc, nil
}

// GenerateModelYAML serialises the document with stable field ordering.
func GenerateModelYAML(doc *ModelDoc) (string, error) {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("YAML generation failed: %w", err)
	}
	return string(out), nil
}

// BuildPolicy renders the default-deny access policy for the given group
// slugs. The admin group is always granted so operators keep full visibility.
func BuildPolicy(allowedGroups []string) []PolicyRule {
	rules := []PolicyRule{{
		Group:       "*",
		MemberLevel: &MemberLevel{Includes: []interface{}{}},
	}}
	seen := map[string]bool{"*": true}
	granted := func(g string) {
		if g == "" || seen[g] {
			return
		}
		seen[g] = true
		rules = append(rules, PolicyRule{Group: g, MemberLevel: &MemberLevel{Includes: "*"}})
	}
	granted(AdminGroup)
	for _, g := range allowedGroups {
		granted(strings.TrimSpace(g))
	}
	return rules
}

// Identity extracts the cube/view name of a parsed document.
func (d *ModelDoc) Identity() (kind, name string, err error) {
	switch {
	case len(d.Cubes) == 1:
		return ModelKindCube, d.Cubes[0].Name, nil
	case len(d.Views) == 1:
		return ModelKindView, d.Views[0].Name, nil
	default:
		return "", "", fmt.Errorf("YAML has no unique primary object")
	}
}

// Validate checks the document against naming conventions and the set of
// model names already visible to Cube (for join target resolution).
func (d *ModelDoc) Validate(knownModelNames map[string]bool) error {
	kind, name, err := d.Identity()
	if err != nil {
		return err
	}
	if !ValidSlug(name) {
		return fmt.Errorf("name %q is invalid: must match ^[a-z][a-z0-9_]*$", name)
	}
	if kind != ModelKindCube {
		return nil // views are YAML-source-only in v1, no further checks
	}
	cube := d.Cubes[0]
	if strings.TrimSpace(cube.SQL) == "" {
		return fmt.Errorf("cube must provide sql (the source table/subquery for the model)")
	}
	if cube.DataSource == "" {
		return fmt.Errorf("cube must specify data_source (the database connection)")
	}
	if err := validateMembers(cube.Measures, "measure"); err != nil {
		return err
	}
	if err := validateMembers(cube.Dimensions, "dimension"); err != nil {
		return err
	}
	if err := validateMembers(cube.Segments, "segment"); err != nil {
		return err
	}
	for _, j := range cube.Joins {
		if !ValidSlug(j.Name) {
			return fmt.Errorf("join name %q is invalid", j.Name)
		}
		if strings.TrimSpace(j.SQL) == "" {
			return fmt.Errorf("join %s is missing its sql condition", j.Name)
		}
		rel := j.Relationship
		if rel == "" {
			return fmt.Errorf("join %s is missing its relationship", j.Name)
		}
		switch rel {
		case "one_to_one", "one_to_many", "many_to_one", "many_to_many":
		default:
			return fmt.Errorf("join %s relationship %q is invalid "+
				"(one_to_one/one_to_many/many_to_many/many_to_one)", j.Name, rel)
		}
		if !knownModelNames[j.Name] {
			return fmt.Errorf("join %s references model %s which does not exist or is not published", j.Name, j.Name)
		}
	}
	return nil
}

func validateMembers(members []MemberDef, kind string) error {
	for _, m := range members {
		if !ValidSlug(m.Name) {
			return fmt.Errorf("%s name %q is invalid", kind, m.Name)
		}
		if kind == "segment" {
			continue // segments are boolean expressions, no type
		}
		if m.Type == "" {
			return fmt.Errorf("%s %s is missing its type", kind, m.Name)
		}
	}
	return nil
}

// KnownModelNames builds the join-target lookup set from the known model
// names plus the one being validated (self-joins are legal).
func KnownModelNames(known map[string]bool, current *ModelDoc) map[string]bool {
	out := make(map[string]bool, len(known)+2)
	for name := range known {
		out[name] = true
	}
	if current != nil {
		if _, name, err := current.Identity(); err == nil {
			out[name] = true
		}
	}
	return out
}

// ---- Draft generation from inspected database schema ----

// ColumnSchema aliases dbinspector's inspected column.
type ColumnSchema = dbinspector.ColumnSchema

// TableSchema is one inspected table.
type TableSchema struct {
	Schema  string         `json:"schema"`
	Name    string         `json:"name"`
	Columns []ColumnSchema `json:"columns"`
}

// CubeTypeForColumn maps a physical column type to a Cube dimension type.
func CubeTypeForColumn(dataType string) string {
	t := strings.ToLower(dataType)
	switch {
	// boolean first: tinyint(1) is the MySQL boolean spelling
	case strings.Contains(t, "bool"), t == "tinyint(1)":
		return "boolean"
	case strings.Contains(t, "int"), strings.Contains(t, "decimal"),
		strings.Contains(t, "numeric"), strings.Contains(t, "float"),
		strings.Contains(t, "double"), strings.Contains(t, "real"),
		strings.Contains(t, "number"):
		return "number"
	case strings.Contains(t, "date"), strings.Contains(t, "time"):
		return "time"
	case strings.Contains(t, "char"), strings.Contains(t, "text"),
		strings.Contains(t, "enum"), strings.Contains(t, "uuid"),
		strings.Contains(t, "string"), strings.Contains(t, "clob"):
		return "string"
	default:
		return "string"
	}
}

// BuildDraftCube generates a starting-point cube from an inspected table:
// a count measure, one dimension per column (typed), a primary key, and the
// soft-delete filter when the table has a deleted_at column — matching the
// modelling conventions already used by the hand-written models.
func BuildDraftCube(connSlug string, table TableSchema) (*ModelDoc, error) {
	tableRef := table.Name
	if table.Schema != "" {
		tableRef = table.Schema + "." + table.Name
	}
	name := Slugify(strings.TrimPrefix(table.Name, table.Schema+"_"))
	if name == "" {
		return nil, fmt.Errorf("cannot derive a cube name from table %s", table.Name)
	}

	sqlText := "SELECT * FROM " + tableRef
	hasDeletedAt := false
	for _, col := range table.Columns {
		if col.Name == "deleted_at" {
			hasDeletedAt = true
			break
		}
	}
	if hasDeletedAt {
		sqlText += " WHERE deleted_at IS NULL"
	}

	cube := CubeDef{
		Name:       name,
		Title:      table.Name,
		SQL:        sqlText,
		DataSource: connSlug,
		Measures: []MemberDef{
			{Name: "count", Type: "count", Title: table.Name + " count"},
		},
	}
	for _, col := range table.Columns {
		if col.Name == "deleted_at" {
			continue
		}
		dim := MemberDef{
			Name:  Slugify(col.Name),
			SQL:   col.Name,
			Type:  CubeTypeForColumn(col.DataType),
			Title: col.Name,
		}
		if col.PrimaryKey {
			dim.PrimaryKey = true
		}
		cube.Dimensions = append(cube.Dimensions, dim)
	}
	doc := &ModelDoc{Cubes: []CubeDef{cube}}
	if _, err := GenerateModelYAML(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// Slugify converts arbitrary text into a valid slug; empty result means the
// input carried no usable characters.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastUnderscore := true // suppress leading underscore
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	// must start with a letter
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "t_" + out
	}
	if len(out) > 63 {
		out = out[:63]
	}
	if !ValidSlug(out) {
		return ""
	}
	return out
}

// SortedKeys is a small helper for deterministic error messages.
func SortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
