package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// stubPreprocessor stands in for the document service: it answers with the
// bytes it was told to answer with, records how often it was asked, and can be
// made to block so a test can drive the caller's deadline.
type stubPreprocessor struct {
	calls     int
	rectified []byte
	fallback  bool
	err       error
	// block, when non-nil, holds every call until it is closed or the caller's
	// context ends — the shape a hung document service has.
	block chan struct{}
	// budget records the deadline the caller set for the call, if any.
	budget time.Duration
	hadEnd bool
}

func (s *stubPreprocessor) PreprocessImage(ctx context.Context, _ []byte) ([]byte, bool, error) {
	s.calls++
	if deadline, ok := ctx.Deadline(); ok {
		s.hadEnd = true
		s.budget = time.Until(deadline)
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	return s.rectified, s.fallback, s.err
}

// preprocessRun builds the run context the preprocessing action reads: one
// pipeline's own mode setting, the attributes an observation would have left
// behind, and whichever preprocessor the deployment has (nil when the document
// service is not wired in).
func preprocessRun(mode string, p imagePreprocessor, attrs types.ImageAttrs) *runContext {
	return &runContext{
		payload:      &types.ImageMultimodalPayload{ImageURL: "stub://image"},
		pipelineID:   string(types.ImagePipelineSmartOCR),
		params:       map[string]any{types.SlotKey(string(types.ImagePipelineSmartOCR), types.PreprocessModeKey): mode},
		declared:     imagePipelineRegistry[types.ImagePipelineSmartOCR].Fields(),
		imageBytes:   []byte("original bytes"),
		imageInfo:    &types.ImageInfo{Attrs: attrs},
		out:          types.JSONMap{},
		preprocessor: p,
	}
}

// textPage is the observation of a photographed page that has text, fills the
// frame and already reads upright — the one an auto run has nothing to gain on.
func textPage() types.ImageAttrs {
	return types.ImageAttrs{Attrs: map[string]any{
		"contain.text":   "block",
		"layout.margin":  "none",
		"layout.upright": true,
	}}
}

func TestPreprocessRequiresExplicitOptIn(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{},
		{"default.preprocess.mode": types.PreprocessModeAlways},
	} {
		stub := &stubPreprocessor{rectified: []byte("changed")}
		r := preprocessRun("", stub, types.ImageAttrs{Attrs: map[string]any{
			"contain.text": "block", "layout.margin": "large", "layout.upright": false,
		}})
		r.params = params
		if got := r.preprocessMode(); got != types.PreprocessModeOff {
			t.Fatalf("unset preprocessing mode = %q, want off", got)
		}
		if types.PreprocessObservesGeometry(r.preprocessMode()) {
			t.Fatal("an untouched knowledge base must not request geometry observations")
		}
		if err := runPreprocessAction(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		if stub.calls != 0 || string(r.imageBytes) != "original bytes" {
			t.Fatal("an untouched knowledge base must not preprocess its images")
		}
		if got := r.out[keyPreprocessResult]; got != "skipped_off" {
			t.Fatalf("preprocess.result = %v, want skipped_off", got)
		}
	}
}

// TestPreprocessSkipReasonFollowsTheSetting pins which reason a skipped
// preprocessing step reports. The vocabulary exists so a reader of a trace can
// tell "the user switched this off" from "this deployment cannot do it", so the
// order the branches are asked in is part of the contract, not an
// implementation detail: answering the capability check first would report a
// missing document service over the choice the user actually made.
func TestPreprocessSkipReasonFollowsTheSetting(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		wired       bool
		attrs       types.ImageAttrs
		wantResult  string
		wantCalls   int
		wantRectify bool
	}{
		{
			// The reason the order is pinned: off + no document service used to
			// report skipped_no_capability, which is not the setting in force.
			name:       "off is reported even where the build has no preprocessor",
			mode:       types.PreprocessModeOff,
			wired:      false,
			wantResult: "skipped_off",
		},
		{
			name:       "an unwired preprocessor is reported when the mode asked for work",
			mode:       types.PreprocessModeAuto,
			wired:      false,
			wantResult: "skipped_no_capability",
		},
		{
			name:       "always asks for work as well",
			mode:       types.PreprocessModeAlways,
			wired:      false,
			wantResult: "skipped_no_capability",
		},
		{
			name:       "off never calls a wired preprocessor",
			mode:       types.PreprocessModeOff,
			wired:      true,
			wantResult: "skipped_off",
		},
		{
			name:       "auto spends nothing on a page the observation found no text in",
			mode:       types.PreprocessModeAuto,
			wired:      true,
			attrs:      types.ImageAttrs{Attrs: map[string]any{"contain.text": "none"}},
			wantResult: "skipped_no_text",
		},
		{
			name:       "auto leaves an already tidy page alone",
			mode:       types.PreprocessModeAuto,
			wired:      true,
			attrs:      textPage(),
			wantResult: "skipped_already_tidy",
		},
		{
			name:        "auto straightens a page the observation said needs it",
			mode:        types.PreprocessModeAuto,
			wired:       true,
			attrs:       types.ImageAttrs{Attrs: map[string]any{"contain.text": "block", "layout.margin": "large"}},
			wantResult:  "done",
			wantCalls:   1,
			wantRectify: true,
		},
		{
			name:        "always skips the question the auto mode asks",
			mode:        types.PreprocessModeAlways,
			wired:       true,
			attrs:       types.ImageAttrs{Attrs: map[string]any{"contain.text": "none"}},
			wantResult:  "done",
			wantCalls:   1,
			wantRectify: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p imagePreprocessor
			var stub *stubPreprocessor
			if tc.wired {
				stub = &stubPreprocessor{rectified: []byte("rectified bytes")}
				p = stub
			}
			r := preprocessRun(tc.mode, p, tc.attrs)
			if err := runPreprocessAction(context.Background(), r); err != nil {
				t.Fatalf("runPreprocessAction returned an error: %v", err)
			}
			if got := r.out[keyPreprocessResult]; got != tc.wantResult {
				t.Errorf("preprocess.result = %v, want %q", got, tc.wantResult)
			}
			if stub != nil && stub.calls != tc.wantCalls {
				t.Errorf("the preprocessor was asked %d times, want %d", stub.calls, tc.wantCalls)
			}
			if tc.wantRectify {
				if got := string(r.imageBytes); got != "rectified bytes" {
					t.Errorf("the run kept %q, want the preprocessed bytes", got)
				}
				if got := r.out[keyPreprocessBytes]; got != len("rectified bytes") {
					t.Errorf("preprocess.bytes = %v, want %d", got, len("rectified bytes"))
				}
				return
			}
			if got := string(r.imageBytes); got != "original bytes" {
				t.Errorf("the run replaced its image with %q, want the original left untouched", got)
			}
		})
	}
}

// TestPreprocessCallCarriesItsOwnDeadline pins the guard against a document
// service that never answers: the call is capped rather than left to inherit the
// whole document's budget, and a call that runs past the cap is recorded like
// any other failure with the image left as it was, so OCR still runs.
//
// The run's own timeout is set small here rather than waiting out the package
// default, which is several times the measured worst case.
func TestPreprocessCallCarriesItsOwnDeadline(t *testing.T) {
	page := types.ImageAttrs{Attrs: map[string]any{"contain.text": "block", "layout.margin": "large"}}

	t.Run("a call that answers in time", func(t *testing.T) {
		stub := &stubPreprocessor{rectified: []byte("rectified bytes")}
		r := preprocessRun(types.PreprocessModeAuto, stub, page)
		if err := runPreprocessAction(context.Background(), r); err != nil {
			t.Fatalf("runPreprocessAction returned an error: %v", err)
		}
		if !stub.hadEnd {
			t.Fatal("the preprocessor was called without a deadline of its own")
		}
		if stub.budget <= 0 || stub.budget > imagePreprocessTimeout {
			t.Errorf("the call's budget was %v, want (0, %v]", stub.budget, imagePreprocessTimeout)
		}
	})

	t.Run("a call that never answers", func(t *testing.T) {
		stub := &stubPreprocessor{rectified: []byte("rectified bytes"), block: make(chan struct{})}
		defer close(stub.block)
		r := preprocessRun(types.PreprocessModeAuto, stub, page)
		r.preprocessTimeout = 20 * time.Millisecond
		if err := runPreprocessAction(context.Background(), r); err != nil {
			t.Fatalf("runPreprocessAction returned an error: %v", err)
		}
		if got := r.out[keyPreprocessResult]; got != "failed" {
			t.Errorf("preprocess.result = %v, want %q", got, "failed")
		}
		if _, written := r.out[keyPreprocessError]; !written {
			t.Error("a call cut short by the deadline left no reason behind")
		}
		if string(r.imageBytes) != "original bytes" {
			t.Errorf("the run replaced its image with %q, want the original", r.imageBytes)
		}
	})
}

// TestPreprocessDeclinesWithoutChangingTheImage pins the one promise the
// preprocessing step makes: every way of not straightening the page leaves the
// run with the bytes it already had, so OCR reads the original photograph
// rather than something worse.
func TestPreprocessDeclinesWithoutChangingTheImage(t *testing.T) {
	cases := []struct {
		name       string
		stub       *stubPreprocessor
		wantResult string
	}{
		{
			name:       "a verdict of not-a-page",
			stub:       &stubPreprocessor{fallback: true},
			wantResult: "skipped_declined",
		},
		{
			name:       "an empty answer",
			stub:       &stubPreprocessor{rectified: nil},
			wantResult: "skipped_declined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := preprocessRun(types.PreprocessModeAlways, tc.stub,
				types.ImageAttrs{Attrs: map[string]any{"contain.text": "block"}})
			if err := runPreprocessAction(context.Background(), r); err != nil {
				t.Fatalf("runPreprocessAction returned an error: %v", err)
			}
			got, _ := r.out[keyPreprocessResult].(string)
			if got != tc.wantResult {
				t.Errorf("preprocess.result = %q, want %q", got, tc.wantResult)
			}
			if string(r.imageBytes) != "original bytes" {
				t.Errorf("the run replaced its image with %q, want the original", r.imageBytes)
			}
		})
	}
}
