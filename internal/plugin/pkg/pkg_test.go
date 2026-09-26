package pkg

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

const tenantSchema = `type: object
properties:
  api_key: { type: string, title: API key, x-secret: true }
required: [api_key]
`

const kitManifest = `schemaVersion: 1
id: acme.kit
version: 1.0.0
name: { en-US: ACME Kit, zh-CN: ACME 工具包 }
publisher: { id: acme }
runtime: { type: declarative }
config: { tenant: config/tenant.yaml }
contributes:
  skills:
    - { id: triage, name: Triage, path: skills/triage }
  mcpServers:
    - id: search
      name: ACME Search
      mcp: { url: "https://mcp.acme.example/mcp", headers: { Authorization: "Bearer ${config.api_key}" } }
`

// zipOf builds an archive from name → content; a nil content adds a directory.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestOpenValidPackage(t *testing.T) {
	data := zipOf(t, map[string]string{
		"plugin.yaml":             kitManifest,
		"config/tenant.yaml":      tenantSchema,
		"skills/triage/SKILL.md":  "---\nname: triage\n---\nTriage issues.",
		"skills/triage/notes.txt": "x",
	})
	p, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Manifest.ID != "acme.kit" || !strings.HasPrefix(p.Digest, "sha256:") || p.Size != int64(len(data)) {
		t.Fatalf("unexpected package: %+v", p)
	}
	if got := p.Files("skills/triage"); strings.Join(got, ",") != "skills/triage/SKILL.md,skills/triage/notes.txt" {
		t.Fatalf("Files = %v", got)
	}
	if !strings.Contains(string(p.Manifest.Config.TenantSchema), `"x-secret":true`) {
		t.Fatalf("tenant schema = %s", p.Manifest.Config.TenantSchema)
	}
	mcp := p.Manifest.Contributes[manifest.PointMCPServers][0].MCP
	if mcp.Headers["Authorization"] != "Bearer ${config.api_key}" {
		t.Fatalf("mcp = %+v", mcp)
	}
	again, _ := Open(data)
	if again.Digest != p.Digest {
		t.Fatal("digest must be stable")
	}
}

func TestOpenStripsSingleRootDirectory(t *testing.T) {
	p, err := Open(zipOf(t, map[string]string{
		"acme-kit-main/plugin.yaml":            kitManifest,
		"acme-kit-main/config/tenant.yaml":     tenantSchema,
		"acme-kit-main/skills/triage/SKILL.md": "x",
	}))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := p.ReadFile("skills/triage/SKILL.md"); !ok {
		t.Fatal("the shared root directory should become the package root")
	}
}

func TestOpenRejectsBadPackages(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		raw   []byte
		want  string
	}{
		"not a zip":   {raw: []byte("hello"), want: "not a zip"},
		"no manifest": {files: map[string]string{"README.md": "x"}, want: "no plugin.yaml"},
		"zip slip":    {files: map[string]string{"plugin.yaml": kitManifest, "../evil": "x"}, want: "escapes"},
		"absolute":    {files: map[string]string{"plugin.yaml": kitManifest, "/etc/passwd": "x"}, want: "absolute"},
		"missing skill": {
			files: map[string]string{"plugin.yaml": kitManifest, "config/tenant.yaml": tenantSchema},
			want:  "skill skills/triage/SKILL.md is not in the package",
		},
		"invalid manifest": {files: map[string]string{"plugin.yaml": "schemaVersion: 1\nid: Bad\n"}, want: "id"},
		"declarative with code point": {
			files: map[string]string{
				"plugin.yaml": strings.Replace(kitManifest,
					"contributes:\n", "contributes:\n  connectors:\n    - { id: jira, name: Jira }\n", 1),
				"config/tenant.yaml":     tenantSchema,
				"skills/triage/SKILL.md": "x",
			},
			want: "contributes.connectors needs code",
		},
	}
	for name, c := range cases {
		data := c.raw
		if data == nil {
			data = zipOf(t, c.files)
		}
		_, err := Open(data)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, c.want)
		}
	}
}

func TestManifestRejectsEscapingPaths(t *testing.T) {
	bad := strings.Replace(kitManifest, "path: skills/triage", "path: ../skills", 1)
	if _, err := manifest.Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "inside the package") {
		t.Fatalf("want path error, got %v", err)
	}
}

func TestOpenCarriesTheIcon(t *testing.T) {
	withIcon := func(icon, file, content string) *Package {
		t.Helper()
		m := strings.Replace(kitManifest, "publisher: { id: acme }", "publisher: { id: acme }\nicon: "+icon, 1)
		p, err := Open(zipOf(t, map[string]string{
			"plugin.yaml": m, "config/tenant.yaml": tenantSchema, "skills/triage/SKILL.md": "x", file: content,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg"/>`
	if got := withIcon("icon.svg", "icon.svg", svg).Manifest.IconData; got != "data:image/svg+xml;base64,"+
		base64.StdEncoding.EncodeToString([]byte(svg)) {
		t.Fatalf("icon = %q", got)
	}
	if got := withIcon("icon.svg", "icon.svg", strings.Repeat("x", maxIconBytes+1)).Manifest.IconData; got != "" {
		t.Fatal("an oversized icon is left out")
	}
	if got := withIcon("icon.gif", "icon.gif", "GIF89a").Manifest.IconData; got != "" {
		t.Fatal("only image types lists can show are carried")
	}
}
