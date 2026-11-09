package service

import (
	"context"
	"github.com/parquet-go/parquet-go"
	"os"
	"path/filepath"
	"testing"
)

func p09Call(t *testing.T, id string) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("loader panic: %v", r)
			ok = false
		}
	}()
	_, err := (&DatasetService{}).GetDatasetByID(context.Background(), id)
	return err == nil
}
func TestP09OriginalBundled(t *testing.T) {
	old, _ := os.Getwd()
	os.Chdir("../../..")
	defer os.Chdir(old)
	if !p09Call(t, "default") {
		t.Error("bundled sample must load without panic")
	}
}
func TestP09OriginalUnknown(t *testing.T) {
	old, _ := os.Getwd()
	os.Chdir("../../..")
	defer os.Chdir(old)
	if p09Call(t, "hidden") {
		t.Error("unknown dataset accepted")
	}
}
func TestP09OriginalMissing(t *testing.T) {
	old, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(old)
	if p09Call(t, "default") {
		t.Error("missing dataset accepted")
	}
}
func TestP09OriginalDecimal(t *testing.T) {
	type Row struct {
		ID   string `parquet:"id"`
		Text string `parquet:"text"`
	}
	p := filepath.Join(t.TempDir(), "text.parquet")
	if e := parquet.WriteFile(p, []Row{{"42", "text"}}); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("decimal panic: %v", r)
		}
	}()
	rows, e := loadParquet[TextInfo](p)
	if e != nil || len(rows) != 1 || rows[0].ID != 42 {
		t.Errorf("decimal42 decoded as %v: %v", rows, e)
	}
}
func TestP09OriginalCanonical(t *testing.T) {
	d := dataset{queries: map[int64]string{2: "two", 0: "zero"}, corpus: map[int64]string{4: "four", 1: "one"}, answers: map[int64]string{2: "answer", 0: "a"}, qas: map[int64]int64{2: 2, 0: 0}, qrels: map[int64][]int64{2: {4, 1}, 0: {1}}}
	for i := 0; i < 50; i++ {
		pairs := d.Iterate()
		if pairs[0].QID != 0 || pairs[1].PIDs[0] != 1 {
			t.Fatal("noncanonical QID/PID order")
		}
	}
}

func TestP09OriginalMalformedDecimal(t *testing.T) {
	type Row struct {
		ID   string `parquet:"id"`
		Text string `parquet:"text"`
	}
	for _, id := range []string{"+42", "-1", " 42", "abc"} {
		t.Run(id, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "text.parquet")
			if e := parquet.WriteFile(p, []Row{{id, "text"}}); e != nil {
				t.Fatal(e)
			}
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("decoder panic: %v", r)
				}
			}()
			if rows, e := loadParquet[TextInfo](p); e == nil {
				t.Errorf("invalid decimal accepted: %v", rows)
			}
		})
	}
}
func TestP09OriginalRelations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dataset", "samples")
	os.MkdirAll(dir, 0700)
	parquet.WriteFile(filepath.Join(dir, "queries.parquet"), []TextInfo{{1, "query"}})
	parquet.WriteFile(filepath.Join(dir, "answers.parquet"), []TextInfo{{1, "answer"}})
	parquet.WriteFile(filepath.Join(dir, "corpus.parquet"), []TextInfo{{1, "passage"}})
	parquet.WriteFile(filepath.Join(dir, "qas.parquet"), []QaInfo{{1, 1}})
	parquet.WriteFile(filepath.Join(dir, "qrels.parquet"), []RelsInfo{{1, 999}})
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	if p09Call(t, "default") {
		t.Error("missing passage relationship accepted")
	}
}
