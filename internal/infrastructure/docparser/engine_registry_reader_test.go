package docparser

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubRemote stands in for the docreader client. NewReader only ever hands it
// back, so the embedded interface is never called — and would panic if routing
// ever sent a parse here by mistake.
type stubRemote struct{ interfaces.DocReader }

func TestNewReaderRoutesByEngine(t *testing.T) {
	ctx := context.Background()
	remote := &stubRemote{}
	deps := ReaderDeps{Remote: remote}

	cases := []struct {
		name     string
		engine   string
		fileType string
		isURL    bool
		want     any
	}{
		{name: "simple engine", engine: SimpleEngineName, fileType: "md", want: &SimpleFormatReader{}},
		{name: "builtin engine goes remote", engine: BuiltinEngineName, fileType: "md", want: remote},
		{name: "unset engine handles simple formats in Go", fileType: "csv", want: &SimpleFormatReader{}},
		{name: "unset engine sends complex formats to docreader", fileType: "docx", want: remote},
		{name: "default pptx engine is remote markitdown", engine: "markitdown", fileType: "pptx", want: remote},
		{name: "URLs always go to docreader", fileType: "md", isURL: true, want: remote},
		{name: "docreader-only engines fall through", engine: "markitdown", fileType: "docx", want: remote},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, err := NewReader(ctx, tc.engine, tc.fileType, tc.isURL, deps)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			if _, isSimple := tc.want.(*SimpleFormatReader); isSimple {
				if _, ok := reader.(*SimpleFormatReader); !ok {
					t.Fatalf("reader = %T, want *SimpleFormatReader", reader)
				}
				return
			}
			if reader != tc.want {
				t.Fatalf("reader = %T, want the docreader client", reader)
			}
		})
	}
}

// A disconnected docreader has to surface as an error, not as a nil reader the
// caller would dereference.
func TestNewReaderReportsDisconnectedDocReader(t *testing.T) {
	if _, err := NewReader(context.Background(), BuiltinEngineName, "docx", false, ReaderDeps{}); err == nil {
		t.Fatal("NewReader succeeded without a docreader connection, want an error")
	}
}

func TestNewReaderRequiresWeKnoraCloudCredentials(t *testing.T) {
	_, err := NewReader(context.Background(), WeKnoraCloudEngineName, "docx", false, ReaderDeps{
		WeKnoraCloudCredentials: func(context.Context) *types.WeKnoraCloudCredentials { return nil },
	})
	if err == nil {
		t.Fatal("NewReader succeeded without credentials, want an error")
	}
}

// The anydoc engine is only linked into builds tagged `anydoc`; everywhere
// else it must be listed as unavailable and refuse to build a reader, so a
// knowledge base configured for it fails loudly instead of parsing with
// something else.
func TestAnydocEngineFollowsBuildAvailability(t *testing.T) {
	reader, err := NewReader(context.Background(), AnydocEngineName, "docx", false, ReaderDeps{})

	if anydoc.Available() {
		if err != nil {
			t.Fatalf("NewReader: %v", err)
		}
		if _, ok := reader.(*AnydocReader); !ok {
			t.Fatalf("reader = %T, want *AnydocReader", reader)
		}
		return
	}
	if err == nil {
		t.Fatal("NewReader succeeded without the converter linked in, want an error")
	}
}

func TestListAllEnginesIncludesAnydoc(t *testing.T) {
	for _, engine := range ListAllEngines(true, nil, nil) {
		if engine.Name != AnydocEngineName {
			continue
		}
		if engine.Available != anydoc.Available() {
			t.Errorf("anydoc availability = %v, want %v", engine.Available, anydoc.Available())
		}
		if !engine.Available && engine.UnavailableReason == "" {
			t.Error("anydoc is unavailable without a reason to show")
		}
		if len(engine.FileTypes) == 0 {
			t.Error("anydoc lists no file types")
		}
		return
	}
	t.Fatal("anydoc engine not found in the engine list")
}
