package providers

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestExtraFieldsSchemaCoversEveryBuiltin(t *testing.T) {
	for _, d := range Builtins() {
		if len(d.ExtraFields) == 0 {
			continue
		}
		s := ExtraFieldsSchema(d.ExtraFields)
		if err := s.Check(); err != nil {
			t.Errorf("%s: schema invalid: %v", d.ID, err)
		}
		if len(s.Properties) != len(d.ExtraFields) {
			t.Errorf("%s: %d properties for %d fields", d.ID, len(s.Properties), len(d.ExtraFields))
		}
		for _, f := range d.ExtraFields {
			p := s.Properties[f.Key]
			if p == nil {
				t.Errorf("%s: field %s missing", d.ID, f.Key)
				continue
			}
			if p.Secret != (f.Secret || f.Type == "password") {
				t.Errorf("%s.%s: secret = %v", d.ID, f.Key, p.Secret)
			}
		}
		// Defaults are valid values of their own field.
		defaults := map[string]any{}
		for key, p := range s.Properties {
			if p.Default != nil {
				defaults[key] = p.Default
			}
		}
		for _, e := range configschema.Validate(s, defaults) {
			if e.Code != configschema.CodeRequired {
				t.Errorf("%s: default fails validation: %v", d.ID, e)
			}
		}
	}
}

func TestExtraFieldsSchemaMapping(t *testing.T) {
	s := ExtraFieldsSchema([]ExtraField{
		{
			Key: "region", Label: "Region", Labels: map[string]string{"zh-CN": "地域"}, Type: "select", Required: true,
			Options: []ExtraFieldOption{{Label: "Beijing", Labels: map[string]string{"zh-CN": "北京"}, Value: "bj"}},
		},
		{Key: "sk", Label: "Secret Key", Type: "password", ModelTypes: []types.ModelType{types.ModelTypeRerank}},
		{Key: "stream", Label: "Stream", Type: "boolean", Default: "true"},
	})
	region := s.Properties["region"]
	if region.Widget != "select" || len(region.OneOf) != 1 || region.OneOf[0].Const != "bj" ||
		region.OneOf[0].I18n["title"]["zh-CN"] != "北京" || region.I18n["title"]["zh-CN"] != "地域" {
		t.Fatalf("select field = %+v", region)
	}
	if len(s.Required) != 1 || s.Required[0] != "region" {
		t.Fatalf("required = %v", s.Required)
	}
	sk := s.Properties["sk"]
	if !sk.Secret || sk.Widget != "password" ||
		len(sk.ModelTypes) != 1 || sk.ModelTypes[0] != string(types.ModelTypeRerank) {
		t.Fatalf("password field = %+v", sk)
	}
	if errs := configschema.Validate(s, map[string]any{"region": "bj", "stream": "yes"}); len(errs) != 1 ||
		errs[0].Path != "stream" || errs[0].Code != configschema.CodeEnum {
		t.Fatalf("boolean must accept only true/false strings, got %v", errs)
	}
}
