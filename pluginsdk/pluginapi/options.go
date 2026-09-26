package pluginapi

// OptionsPath answers the choices of a form field whose schema names
// x-options {name}: projects of the account the form is being filled for,
// channels of a workspace, and so on. The envelope's config carries what
// the form holds right now (stored secrets filled in for fields the user did
// not change), in the scope the form edits.
func OptionsPath(name string) string { return "/v1/options/" + name }

// Option scopes: which configuration the form edits.
const (
	OptionsScopeSystem   = "system"
	OptionsScopeTenant   = "tenant"
	OptionsScopeInstance = "instance"
)

// OptionsInput asks for one field's choices.
type OptionsInput struct {
	// Field is the dotted path of the field ("settings.project").
	Field string `json:"field"`
	// Scope is system, tenant or instance.
	Scope string `json:"scope"`
	// Contribution is "<point>/<id>" of the instance's contribution
	// ("connectors/jira") when Scope is instance.
	Contribution string `json:"contribution,omitempty"`
	// Query is what the user typed, for fields that search (x-options.search).
	Query string `json:"query,omitempty"`
}

// Option is one choice.
type Option struct {
	Value       any    `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// OptionsOutput lists the choices, in display order.
type OptionsOutput struct {
	Options []Option `json:"options"`
}
