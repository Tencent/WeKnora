package format_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/format"
)

// A malformed --jq expression must surface as *format.JQError so the cmdutil
// layer can map it to a typed input.invalid_argument (exit 5) instead of the
// unclassified internal.error bucket (exit 1). Regression for the bad-jq
// misclassification.
func TestWriteJSONFiltered_BadJQExpr_IsJQError(t *testing.T) {
	for _, expr := range []string{".data[", "this is not jq"} {
		buf := &bytes.Buffer{}
		err := format.WriteJSONFiltered(buf, map[string]any{"data": []any{}}, nil, expr)
		if err == nil {
			t.Fatalf("expr %q: expected error, got nil", expr)
		}
		var jqe *format.JQError
		if !errors.As(err, &jqe) {
			t.Errorf("expr %q: expected *format.JQError, got %T: %v", expr, err, err)
		}
	}
}

// A jq eval failure (valid parse, runtime error) is also expression-attributable
// and must be a *JQError.
func TestWriteJSONFiltered_JQEvalError_IsJQError(t *testing.T) {
	buf := &bytes.Buffer{}
	// ".foo.bar" against a non-object .foo raises an eval error.
	err := format.WriteJSONFiltered(buf, map[string]any{"foo": 5}, nil, ".foo.bar")
	if err == nil {
		t.Fatal("expected eval error, got nil")
	}
	var jqe *format.JQError
	if !errors.As(err, &jqe) {
		t.Errorf("expected *format.JQError, got %T: %v", err, err)
	}
}

// A valid expression must NOT error.
func TestWriteJSONFiltered_ValidJQ_NoError(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := format.WriteJSONFiltered(buf, map[string]any{"data": []any{"a", "b"}}, nil, ".data[]"); err != nil {
		t.Fatalf("valid jq errored: %v", err)
	}
}
