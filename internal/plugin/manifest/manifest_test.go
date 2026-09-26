package manifest

import (
	"encoding/json"
	"fmt"
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
	if err := base("api.example.com", "*.atlassian.net", "*").Validate(); err != nil {
		t.Fatalf("valid egress rejected: %v", err)
	}
	for _, bad := range []string{"**", "http://x.com", "x.com:443", "*.*.com", "10.0.0.0/8"} {
		if err := base(bad).Validate(); err == nil || !strings.Contains(err.Error(), "permissions.egress") {
			t.Errorf("egress %q: got %v", bad, err)
		}
	}
}

func TestStoredTypeIDLength(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion, ID: "acme.a-rather-long-plugin-name", Version: "1.0.0",
		APIVersion: ExtensionAPIVersion, Name: Text("X", nil), Publisher: Publisher{ID: "acme"},
		Runtime:     Runtime{Type: RuntimeHost, Kind: "binary", Entry: "bin/x"},
		Contributes: Contributions{PointConnectors: {{ID: "and-an-even-longer-connector-id", Name: Text("X", nil)}}},
	}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "longer than the 50 characters") {
		t.Fatalf("want a length error, got %v", err)
	}
}

func TestUIContributions(t *testing.T) {
	base := func(c Contribution) *Manifest {
		c.ID, c.Name = "links", Text("Links", nil)
		return &Manifest{
			SchemaVersion: SchemaVersion, ID: "acme.x", Version: "1.0.0",
			Name: Text("X", nil), Publisher: Publisher{ID: "acme"}, Runtime: Runtime{Type: RuntimeDeclarative},
			Contributes: Contributions{PointPages: {c}},
		}
	}
	if err := base(Contribution{Entry: "ui/index.html", MinRole: "contributor"}).Validate(); err != nil {
		t.Fatalf("a declarative plugin may have pages: %v", err)
	}
	for _, c := range []Contribution{
		{}, {Entry: "index.html"}, {Entry: "ui/../main.py"}, {Entry: "ui/app.js"}, {Entry: "ui/x.html", MinRole: "god"},
	} {
		if err := base(c).Validate(); err == nil {
			t.Errorf("%+v should be refused", c)
		}
	}
	if UIMinRole(PointSettingsSections, Contribution{}) != "admin" ||
		UIMinRole(PointPages, Contribution{}) != "viewer" ||
		UIMinRole(PointKBTabs, Contribution{MinRole: "owner"}) != "owner" {
		t.Fatal("min role defaults")
	}
}

func TestResourceAmounts(t *testing.T) {
	for in, want := range map[string]int64{"500m": 500, "0.5": 500, "2": 2000} {
		if got, err := ParseCPU(in); err != nil || got != want {
			t.Errorf("ParseCPU(%q) = %d, %v", in, got, err)
		}
	}
	for in, want := range map[string]int64{"512Mi": 512 << 20, "1Gi": 1 << 30, "500M": 500e6, "1024": 1024} {
		if got, err := ParseMemory(in); err != nil || got != want {
			t.Errorf("ParseMemory(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-1", "lots", "0m"} {
		if _, err := ParseCPU(bad); err == nil {
			t.Errorf("ParseCPU(%q) accepted", bad)
		}
		if _, err := ParseMemory(bad); err == nil {
			t.Errorf("ParseMemory(%q) accepted", bad)
		}
	}
}

func TestToolServersAndViews(t *testing.T) {
	base := `schemaVersion: 1
id: acme.tools
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: Tools }
publisher: { id: acme }
runtime: %s
contributes:
  mcpServers:
    - id: issues
      name: Issues
%s`
	parse := func(runtime, rest string) error {
		_, err := Parse([]byte(fmt.Sprintf(base, runtime, rest)))
		return err
	}
	code := "{ type: host, kind: binary, entry: bin/x }"
	views := `      toolViews:
        search: { view: table, items: issues, columns: [key, { field: summary, title: { en-US: Summary }, link: url }] }
        get: { view: page, entry: ui/issue.html }
        stats: { view: kv }
`
	if err := parse(code, views); err != nil {
		t.Fatalf("a code plugin serving its own tools: %v", err)
	}
	m, _ := Parse([]byte(fmt.Sprintf(base, code, views)))
	c := m.Contributes[PointMCPServers][0]
	if !c.ServedByPlugin() || c.ToolViews["search"].Columns[0].Field != "key" ||
		c.ToolViews["search"].Columns[1].Link != "url" {
		t.Fatalf("contribution = %+v", c)
	}
	for name, tc := range map[string]struct{ runtime, rest, want string }{
		"declarative needs a url": {"{ type: declarative }", "", "mcp.url is required"},
		"headers need a url":      {code, "      mcp: { headers: { X: y } }\n", "only apply to a remote url"},
		"table without columns":   {code, "      toolViews: { search: { view: table } }\n", "a table needs columns"},
		"cards without title":     {code, "      toolViews: { search: { view: cards } }\n", "cards need a title"},
		"page outside ui": {
			code, "      toolViews: { get: { view: page, entry: x.html } }\n", "must be a file under ui/",
		},
		"unknown view": {code, "      toolViews: { get: { view: chart } }\n", "view must be"},
		"bad path": {
			code, "      toolViews: { get: { view: kv, title: \"a[0]\" } }\n", "dotted field path",
		},
	} {
		if err := parse(tc.runtime, tc.rest); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInstanceEditors(t *testing.T) {
	base := `schemaVersion: 1
id: acme.jira
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: Jira }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/x }
contributes:
  %s:
    - { id: jira, name: Jira, editor: %s }
`
	m, err := Parse([]byte(fmt.Sprintf(base, "connectors", "ui/editor.html")))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Contributes[PointConnectors][0]
	if !HasEditor(PointConnectors, c) || UIMinRole(PointConnectors, c) != "admin" {
		t.Fatalf("editor = %+v", c)
	}
	bad := [][2]string{{"connectors", "editor.html"}, {"connectors", "ui/editor.js"}, {"parsers", "ui/editor.html"}}
	for _, tc := range bad {
		if _, err := Parse([]byte(fmt.Sprintf(base, tc[0], tc[1]))); err == nil {
			t.Errorf("%v accepted", tc)
		}
	}
}

func TestPipelineHookStages(t *testing.T) {
	hook := func(stages ...string) *Manifest {
		return &Manifest{
			SchemaVersion: SchemaVersion, ID: "acme.guard", Version: "1.0.0", APIVersion: ExtensionAPIVersion,
			Name: Text("G", nil), Publisher: Publisher{ID: "acme"},
			Runtime:     Runtime{Type: RuntimeRemote},
			Contributes: Contributions{PointPipelineHooks: {{ID: "g", Name: Text("G", nil), Stages: stages}}},
		}
	}
	if err := hook(StageRewriteQuery, StageAnswer).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{nil, {"rerank"}} {
		if err := hook(bad...).Validate(); err == nil {
			t.Fatalf("stages %v accepted", bad)
		}
	}
	m := hook(StageAnswer)
	m.Contributes[PointWebSearch] = []Contribution{{ID: "w", Name: Text("W", nil), Stages: []string{StageAnswer}}}
	if err := m.Validate(); err == nil {
		t.Fatal("stages on a web search provider accepted")
	}
}
