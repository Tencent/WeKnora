package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ole2Header is the compound-document signature a binary BIFF .xls starts with.
var ole2Header = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// writeLegacyXLS writes a file that looks like a binary BIFF .xls: the OLE2
// signature followed by opaque payload bytes. DuckDB's read_xlsx cannot open
// this (there is no xl/workbook.xml inside), which is exactly the case #4239
// reports.
func writeLegacyXLS(t *testing.T, path string) {
	t.Helper()
	payload := append([]byte{}, ole2Header...)
	payload = append(payload, make([]byte, 512)...)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write legacy xls fixture: %v", err)
	}
}

func TestHasLegacyXLSContainerDetectsOLE2Workbook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.xls")
	writeLegacyXLS(t, path)

	if !hasLegacyXLSContainer(path) {
		t.Fatalf("expected %s to be detected as a binary BIFF workbook", path)
	}
}

func TestHasLegacyXLSContainerIgnoresZipBasedWorkbooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "modern.xlsx")
	writeWorkbook(t, path, map[string][][]any{
		"Data": {{"id", "amount"}, {1, 100}},
	}, []string{"Data"})

	// A .xls that is really a renamed .xlsx is a ZIP archive and must keep
	// loading through the normal read_xlsx path.
	if hasLegacyXLSContainer(path) {
		t.Fatalf("expected the ZIP-based workbook %s not to be flagged as legacy", path)
	}
}

func TestHasLegacyXLSContainerLeavesShortAndMissingFilesAlone(t *testing.T) {
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty.xls")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	if hasLegacyXLSContainer(empty) {
		t.Fatalf("an empty file is not a legacy workbook")
	}

	if hasLegacyXLSContainer(filepath.Join(dir, "missing.xls")) {
		t.Fatalf("a missing file must not be reported as a legacy workbook")
	}
}

func TestLoadFromExcelRejectsLegacyBinaryXLSWithActionableError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.xls")
	writeLegacyXLS(t, path)

	// No DuckDB handle: the rejection has to happen before the tool touches
	// the database, otherwise a nil db would panic instead of failing cleanly.
	tool := &DataAnalysisTool{sessionID: "legacy-xls"}

	_, err := tool.LoadFromExcel(context.Background(), path, "dataset")
	if err == nil {
		t.Fatal("expected a binary .xls workbook to be rejected")
	}

	message := err.Error()
	for _, want := range []string{"legacy binary .xls", ".xlsx", ".csv"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q does not mention %q", message, want)
		}
	}
}

func TestDataAnalysisToolDescriptionNamesTheXLSConversion(t *testing.T) {
	description := dataAnalysisTool.description
	if !strings.Contains(description, ".xlsx") || !strings.Contains(description, "binary .xls") {
		t.Fatalf("tool description must tell the model how to handle a legacy .xls, got: %s", description)
	}
}
