package providers

import "github.com/Tencent/WeKnora/internal/plugin/configschema"

// ExtraFieldsSchema describes a vendor's extra fields as a config schema.
//
// The values live in models.parameters.extra_config, a map of strings, so
// every field is a string: number and boolean fields only pick a widget, and
// a boolean is the choice between "true" and "false". A field is secret when
// it says so or is a password input, which is how the model editor already
// treats it.
func ExtraFieldsSchema(fields []ExtraField) *configschema.Schema {
	s := configschema.Object()
	for i, f := range fields {
		prop := &configschema.Schema{
			Type:        configschema.TypeString,
			Title:       f.Label,
			Placeholder: f.Placeholder,
			Secret:      f.Secret || f.Type == "password",
			Order:       i,
		}
		if f.Default != "" {
			prop.Default = f.Default
		}
		addI18n(prop, "title", f.Labels)
		addI18n(prop, "placeholder", f.Placeholders)
		for _, mt := range f.ModelTypes {
			prop.ModelTypes = append(prop.ModelTypes, string(mt))
		}
		switch f.Type {
		case "password":
			prop.Widget = "password"
		case "number":
			prop.Widget = "number"
		case "boolean":
			prop.Widget = "switch"
			prop.OneOf = []*configschema.Schema{{Const: "true"}, {Const: "false"}}
		case "select":
			prop.Widget = "select"
			for _, o := range f.Options {
				choice := &configschema.Schema{Const: o.Value, Title: o.Label}
				addI18n(choice, "title", o.Labels)
				prop.OneOf = append(prop.OneOf, choice)
			}
		}
		s.Set(f.Key, prop, f.Required)
	}
	return s
}

func addI18n(s *configschema.Schema, key string, locales map[string]string) {
	for locale, text := range locales {
		if text == "" {
			continue
		}
		if s.I18n == nil {
			s.I18n = map[string]map[string]string{}
		}
		if s.I18n[key] == nil {
			s.I18n[key] = map[string]string{}
		}
		s.I18n[key][locale] = text
	}
}
