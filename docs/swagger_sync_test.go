package docs_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	docs "github.com/Tencent/WeKnora/docs"
	"github.com/swaggo/swag"
	"github.com/swaggo/swag/gen"
)

func TestSwaggerDocsSync(t *testing.T) {
	repoRoot := findRepoRoot(t)
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir %s: %v", repoRoot, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	outDir := t.TempDir()
	err = gen.New().Build(&gen.Config{
		SearchDir:          "./",
		Excludes:           "./internal/localsandbox",
		MainAPIFile:        "./cmd/server/main.go",
		PropNamingStrategy: swag.CamelCase,
		OutputDir:          outDir,
		OutputTypes:        []string{"json", "yaml"},
		ParseDependency:    1,
		ParseInternal:      true,
		ParseDepth:         100,
		OverridesFile:      "./docs/swag.overrides",
		ParseGoList:        true,
		LeftTemplateDelim:  "{{",
		RightTemplateDelim: "}}",
		CollectionFormat:   "csv",
		Debugger:           log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("generate swagger spec: %v", err)
	}

	for _, name := range []string{"swagger.json", "swagger.yaml"} {
		want, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read generated %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(repoRoot, "docs", name))
		if err != nil {
			t.Fatalf("read committed docs/%s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("docs/%s is out of sync with Go Swagger annotations; run 'make docs'", name)
		}
	}
}

func TestSwaggerInfoReadDocAndOverrides(t *testing.T) {
	rawJSON := readSwaggerFile(t, "swagger.json")
	if got := docs.SwaggerInfo.ReadDoc(); got != string(rawJSON) {
		t.Fatalf("default ReadDoc() length = %d, want exact embedded swagger.json length %d", len(got), len(rawJSON))
	}

	orig := docs.SwaggerInfo.Spec
	origSchemes := append([]string(nil), docs.SwaggerInfo.Schemes...)
	t.Cleanup(func() {
		docs.SwaggerInfo.Spec = orig
		docs.SwaggerInfo.Schemes = origSchemes
	})

	docs.SwaggerInfo.Host = "kb.example.com"
	docs.SwaggerInfo.BasePath = "/subpath/api/v1"
	docs.SwaggerInfo.Schemes = []string{"https"}
	docs.SwaggerInfo.Version = "2.0"
	docs.SwaggerInfo.Title = "Custom Title"
	docs.SwaggerInfo.Description = "Prompt example {{query}}"

	var parsed struct {
		Host     string   `json:"host"`
		BasePath string   `json:"basePath"`
		Schemes  []string `json:"schemes"`
		Info     struct {
			Version     string `json:"version"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(docs.SwaggerInfo.ReadDoc()), &parsed); err != nil {
		t.Fatalf("unmarshal overridden ReadDoc(): %v", err)
	}
	if parsed.Host != "kb.example.com" || parsed.BasePath != "/subpath/api/v1" {
		t.Fatalf("unexpected host/basePath override: %+v", parsed)
	}
	if len(parsed.Schemes) != 1 || parsed.Schemes[0] != "https" {
		t.Fatalf("unexpected schemes override: %+v", parsed.Schemes)
	}
	if parsed.Info.Version != "2.0" ||
		parsed.Info.Title != "Custom Title" ||
		parsed.Info.Description != "Prompt example {{query}}" {
		t.Fatalf("unexpected info override: %+v", parsed.Info)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
