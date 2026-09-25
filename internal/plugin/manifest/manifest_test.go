package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

const jiraManifest = `
schemaVersion: 1
id: acme.jira
version: 1.2.0
apiVersion: weknora.plugin/v1
engines: { weknora: ">=0.10.0" }
name: { zh-CN: Jira 问题, en-US: Jira }
publisher: { id: acme, name: ACME Inc. }
runtime: { type: host, kind: binary, entry: "bin/{os}-{arch}/jira" }
permissions:
  egress: ["*.atlassian.net"]
contributes:
  connectors:
    - id: jira
      name: Jira
      capabilities: [incremental]
      instanceSchema: schemas/connector.json
`

// openToThirdParty opens a point for the duration of a test; no point is open
// to third parties in P0, but the validation path still needs covering.
func openToThirdParty(t *testing.T, p Point) {
	t.Helper()
	saved := points
	points = make([]PointInfo, len(saved))
	copy(points, saved)
	for i := range points {
		if points[i].Point == p {
			points[i].ThirdParty = true
		}
	}
	t.Cleanup(func() { points = saved })
}

func TestParseValidManifest(t *testing.T) {
	openToThirdParty(t, PointConnectors)
	m, err := Parse([]byte(jiraManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ID != "acme.jira" || m.Runtime.Type != RuntimeHost {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if got := m.Name.Resolve("zh-CN"); got != "Jira 问题" {
		t.Fatalf("zh-CN name = %q", got)
	}
	if got := m.Name.Default; got != "Jira" {
		t.Fatalf("default name = %q, want the en-US variant", got)
	}
	c := m.Contributes[PointConnectors]
	if len(c) != 1 || c[0].ID != "jira" || c[0].Name.Default != "Jira" {
		t.Fatalf("unexpected connectors: %+v", c)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	openToThirdParty(t, PointConnectors)
	_, err := Parse([]byte(jiraManifest + "\nruntimee: {}\n"))
	if err == nil || !strings.Contains(err.Error(), "runtimee") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "Bad_ID",
		Version:       "1.0",
		Runtime:       Runtime{Type: RuntimeHost},
		Contributes: Contributions{
			"widgets":       {{ID: "x", Name: Text("X", nil)}},
			PointIMChannels: {{ID: "im", Name: Text("IM", nil)}},
			PointConnectors: {
				{ID: "dup", Name: Text("A", nil), Aliases: []string{"old"}},
				{ID: "dup"},
			},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{
		"schemaVersion must be 1",
		`id "Bad_ID"`,
		`version "1.0"`,
		"name is required",
		"runtime.kind",
		"runtime.entry",
		"apiVersion must be",
		"contributes.widgets is not a known extension point",
		"contributes.imChannels is not open to third-party plugins yet",
		`"dup" is declared twice`,
		"contributes.connectors[1].name is required",
		"aliases may only be declared by builtin plugins",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBuiltinPublisherIsReserved(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		ID:            "weknora.fake",
		Version:       "1.0.0",
		Name:          Text("Fake", nil),
		Publisher:     Publisher{ID: BuiltinPublisher},
		Runtime:       Runtime{Type: RuntimeBuiltin},
		Contributes:   Contributions{PointTools: {{ID: "t", Name: Text("T", nil)}}},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "reserved for builtin plugins") {
		t.Fatalf("want reserved-publisher error, got %v", err)
	}
	m.Builtin = true
	if err := m.Validate(); err != nil {
		t.Fatalf("builtin manifest should validate: %v", err)
	}
}

func TestStrictSemver(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.0": true, "1.2.0-beta.1": true, "1.2.0+build.5": true,
		"1.2": false, "v1.2.0": false, "01.2.0": false, "": false,
	} {
		if got := isStrictSemver(v); got != want {
			t.Errorf("isStrictSemver(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestLocalizedTextResolve(t *testing.T) {
	text := Text("Feishu", map[string]string{"zh-CN": "飞书", "ja-JP": ""})
	cases := map[string]string{
		"zh-CN": "飞书",
		"zh-TW": "飞书", // same language
		"zh":    "飞书",
		"en-US": "Feishu",
		"ja-JP": "Feishu", // empty variants are dropped
		"":      "Feishu",
	}
	for locale, want := range cases {
		if got := text.Resolve(locale); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", locale, got, want)
		}
	}
}

func TestLocalizedTextJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(Text("Feishu", map[string]string{"zh-CN": "飞书"}))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"default":"Feishu","zh-CN":"飞书"}` {
		t.Fatalf("marshal = %s", data)
	}
	var back LocalizedText
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Default != "Feishu" || back.Locales["zh-CN"] != "飞书" {
		t.Fatalf("round trip = %+v", back)
	}
	var plain LocalizedText
	if err := json.Unmarshal([]byte(`"Jira"`), &plain); err != nil || plain.Default != "Jira" {
		t.Fatalf("plain string = %+v, %v", plain, err)
	}
}

func TestContributionOmitsEmptyDescription(t *testing.T) {
	data, err := json.Marshal(Contribution{ID: "x", Name: Text("X", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "description") {
		t.Fatalf("empty description should be omitted: %s", data)
	}
}

func TestEgressPatterns(t *testing.T) {
	base := func(egress ...string) *Manifest {
		return &Manifest{
			SchemaVersion: SchemaVersion, ID: "acme.x", Version: "1.0.0", APIVersion: ExtensionAPIVersion,
			Name: Text("X", nil), Publisher: Publisher{ID: "acme"},
			Runtime:     Runtime{Type: RuntimeHost, Kind: "binary", Entry: "bin/x"},
			Permissions: Permissions{Egress: egress},
			Contributes: Contributions{PointWebSearch: {{ID: "x", Name: Text("X", nil)}}},
		}
	}
	if err := base("api.example.com", "*.atlassian.net").Validate(); err != nil {
		t.Fatalf("valid egress rejected: %v", err)
	}
	for _, bad := range []string{"*", "http://x.com", "x.com:443", "*.*.com", "10.0.0.0/8"} {
		if err := base(bad).Validate(); err == nil || !strings.Contains(err.Error(), "permissions.egress") {
			t.Errorf("egress %q: got %v", bad, err)
		}
	}
}
