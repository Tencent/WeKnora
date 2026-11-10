package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func TestLoadFromCSVTextEncodings(t *testing.T) {
	chinese := "id,name,note\r\n001,苹果,\"第一行\n第二行\"\r\n002,香蕉,正常\r\n"
	encode := func(enc encoding.Encoding, value string) []byte {
		t.Helper()
		data, err := enc.NewEncoder().Bytes([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tests := []struct {
		name               string
		data               []byte
		wantName, wantNote string
	}{
		{"GBK", encode(simplifiedchinese.GBK, chinese), "苹果", "第一行\n第二行"},
		{"GB18030", encode(simplifiedchinese.GB18030, strings.ReplaceAll(chinese, "苹果", "苹果🍎")), "苹果🍎", "第一行\n第二行"},
		{"UTF16LE", encode(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), chinese), "苹果", "第一行\n第二行"},
		{"UTF16BE", encode(unicode.UTF16(unicode.BigEndian, unicode.UseBOM), chinese), "苹果", "第一行\n第二行"},
		{"UTF8", []byte(chinese), "苹果", "第一行\n第二行"},
		{"UTF8 BOM", append([]byte{0xef, 0xbb, 0xbf}, []byte(chinese)...), "苹果", "第一行\n第二行"},
		{
			"Windows1252",
			[]byte("id,name,note\n001,caf\xe9,\x93Quoted\x94 text \x96 costs 5 \x80.\n002,other,normal\n"),
			"café", "“Quoted” text – costs 5 €.",
		},
		{"Latin1", []byte("id,name,note\n001,caf\xe9,\x81\n002,other,normal\n"), "café", "\u0081"},
		{
			"late GBK",
			encode(simplifiedchinese.GBK, "id,name,note\n001,apple,"+strings.Repeat("a", 128*1024)+"中文\n002,香蕉,正常\n"),
			"apple", strings.Repeat("a", 128*1024) + "中文",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.csv")
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			tool := &DataAnalysisTool{db: newPlainDuckDB(t), sessionID: "encoding-test"}
			t.Cleanup(func() { tool.Cleanup(ctx) })
			schema, err := tool.LoadFromCSV(ctx, path, "encoded")
			if err != nil {
				t.Fatalf("LoadFromCSV: %v", err)
			}
			if schema.RowCount != 2 {
				t.Fatalf("row count=%d, want 2", schema.RowCount)
			}
			var id, name, note string
			err = tool.db.QueryRowContext(ctx, `SELECT id,name,note FROM encoded WHERE id='001'`).
				Scan(&id, &name, &note)
			if err != nil || id != "001" || name != tt.wantName || note != tt.wantNote {
				t.Fatalf("cell data mismatch: id=%q name=%q note length=%d err=%v", id, name, len(note), err)
			}
			for _, column := range schema.Columns {
				if column.Type != "VARCHAR" {
					t.Errorf("column %s type=%s, want VARCHAR", column.Name, column.Type)
				}
			}
			original, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, tt.data) {
				t.Fatal("loading changed the original file")
			}
		})
	}
}

func TestDataAnalysisRegistryLoadsGBKKnowledge(t *testing.T) {
	data, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("id,name\n001,苹果\n002,香蕉\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc := &types.Knowledge{ID: "gbk-doc", FileType: "csv", FilePath: "source.csv"}
	files := &csvFileService{files: map[string]string{doc.FilePath: string(data)}}
	tool := NewDataAnalysisTool(nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
		nil, files, newPlainDuckDB(t), "gbk-registry-test")
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	args, err := json.Marshal(DataAnalysisInput{
		KnowledgeID: doc.ID,
		SQL:         `SELECT id,name FROM dataset WHERE name='苹果'`,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.ExecuteTool(ctx, ToolDataAnalysis, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("ExecuteTool: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, `"id":"001"`) || !strings.Contains(result.Output, `"name":"苹果"`) {
		t.Fatalf("unexpected query output: %s", result.Output)
	}
	if files.files[doc.FilePath] != string(data) {
		t.Fatal("loading changed the stored file")
	}
}

func TestCSVPreparationLifecycle(t *testing.T) {
	data, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("id,name\n001,苹果\n"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.csv")
	outputDir := t.TempDir()
	t.Setenv("TMPDIR", outputDir)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	converted, cleanup, err := prepareCSVForDuckDB(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if converted == path {
		t.Fatal("legacy input must use a separate file")
	}
	info, err := os.Stat(converted)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary CSV must be private: info=%v err=%v", info, err)
	}
	cleanup()
	cleanup()
	if _, err := os.Stat(converted); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary CSV survived cleanup: %v", err)
	}
	// A rejected CREATE must release the converted copy too.
	ctx := context.Background()
	tool := &DataAnalysisTool{db: newPlainDuckDB(t), sessionID: "encoding-failure-test"}
	t.Cleanup(func() { tool.Cleanup(ctx) })
	if _, err := tool.db.ExecContext(ctx, "CREATE TABLE occupied (id VARCHAR)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.LoadFromCSV(ctx, path, "occupied"); err == nil {
		t.Fatal("CREATE unexpectedly replaced an existing table")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := prepareCSVForDuckDB(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary files leaked: entries=%v err=%v", entries, err)
	}
	// UTF-8 stays on its original path without creating a temporary copy.
	if err := os.WriteFile(path, []byte("id,name\n001,苹果\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	same, cleanup, err := prepareCSVForDuckDB(ctx, path)
	if err != nil || same != path {
		t.Fatalf("UTF-8 path changed: %q err=%v", same, err)
	}
	cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("UTF-8 cleanup removed the source: %v", err)
	}
}
