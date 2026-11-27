package docparser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestPaddleOCRVLNativePDFRouting(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, name := range []string{
		"native", "mixed", "missing marker", "scanned metadata", "empty", "nil", "error field", "transport error",
		"default ocr", "explicit ocr", "request auto", "request ocr", "force scanned",
		"image", "disconnected", "missing remote",
	} {
		t.Run(name, func(t *testing.T) {
			paddleCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				paddleCalls++
				if req.URL.Path != "/layout-parsing" {
					t.Errorf("unexpected path %s", req.URL.Path)
				}
				_, _ = io.WriteString(w, `{"errorCode":0,"result":{"layoutParsingResults":[
 {"markdown":{"text":"Paddle layout output","images":{}}}]}}`)
			}))
			defer server.Close()
			native := &types.ReadResult{
				MarkdownContent: "| Region | Sales |\n| --- | --- |\n| North | 100 |\n![figure](images/figure.jpg)",
				ImageRefs: []types.ImageRef{{
					Filename: "figure.jpg", OriginalRef: "images/figure.jpg", ImageData: []byte("figure bytes"),
				}},
				ImageDirPath: "/images/request-id",
				SourceBlocks: []types.SourceBlock{{Start: 0, End: 10}},
				Metadata: map[string]string{
					"pdf_native_only": "accepted", "image_source_type": "pdf_text_layer",
				},
			}
			stub := &stubDocReader{result: native}
			overrides := map[string]string{"paddleocr_vl_endpoint": server.URL, "paddleocr_vl_pdf_mode": "auto"}
			req := &types.ReadRequest{
				FileName: "report.PDF", FileType: "pdf", FileContent: []byte("original PDF bytes"),
				ParserEngine: PaddleOCRVLEngineName, RequestID: "original-request-id",
				ParserEngineOverrides: map[string]string{"keep_me": "yes"},
			}
			deps := ReaderDeps{Overrides: overrides, Remote: stub}
			wantNative, wantProbe := false, true
			switch name {
			case "native":
				wantNative = true
			case "mixed":
				native.Metadata["pdf_native_only"] = "requires_ocr"
			case "missing marker":
				delete(native.Metadata, "pdf_native_only")
			case "scanned metadata":
				native.Metadata["image_source_type"] = "scanned_pdf"
			case "empty":
				native.MarkdownContent = "  "
			case "nil":
				stub.result = nil
			case "error field":
				native.Error = "parse failed"
			case "transport error":
				stub.err = errors.New("docreader unavailable")
			case "default ocr":
				delete(overrides, "paddleocr_vl_pdf_mode")
				wantProbe = false
			case "explicit ocr":
				overrides["paddleocr_vl_pdf_mode"] = "ocr"
				wantProbe = false
			case "request auto":
				delete(overrides, "paddleocr_vl_pdf_mode")
				req.ParserEngineOverrides["paddleocr_vl_pdf_mode"] = "auto"
				wantNative = true
			case "request ocr":
				req.ParserEngineOverrides["paddleocr_vl_pdf_mode"] = "ocr"
				wantProbe = false
			case "force scanned":
				req.ParserEngineOverrides["pdf_force_scanned"] = "true"
				wantProbe = false
			case "image":
				wantProbe = false
				req.FileType = "png"
				req.FileName = "image.png"
			case "disconnected":
				deps.Remote = disconnectedDocReader{}
				wantProbe = false
			case "missing remote":
				deps.Remote = nil
				wantProbe = false
			}
			original := *req
			original.ParserEngineOverrides = map[string]string{}
			for k, v := range req.ParserEngineOverrides {
				original.ParserEngineOverrides[k] = v
			}
			reader, err := (&paddleOCRVLEngine{}).NewReader(context.Background(), deps)
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.Read(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if wantNative {
				if got != native || paddleCalls != 0 {
					t.Fatalf("native result lost or OCR called: result=%+v calls=%d", got, paddleCalls)
				}
			} else if paddleCalls != 1 || !strings.Contains(got.MarkdownContent, "Paddle layout output") {
				t.Fatalf("whole-document Paddle fallback missing: result=%+v calls=%d", got, paddleCalls)
			}
			if wantProbe {
				if stub.got == nil {
					t.Fatal("DocReader native probe was never called")
				}
				if stub.got.ParserEngine != BuiltinEngineName ||
					stub.got.ParserEngineOverrides["pdf_native_only"] != "true" {
					t.Fatalf("incorrect native probe: %+v", stub.got)
				}
				if string(stub.got.FileContent) != string(req.FileContent) ||
					stub.got.RequestID != req.RequestID || stub.got.ParserEngineOverrides["keep_me"] != "yes" {
					t.Fatal("probe lost original content, identity or overrides")
				}
			} else if stub.got != nil {
				t.Fatal("unexpected DocReader call")
			}
			if !reflect.DeepEqual(req, &original) {
				t.Fatal("Read mutated caller request/overrides")
			}
		})
	}
}

type cancellingNativeReader struct {
	cancel context.CancelFunc
	result *types.ReadResult
}

func (s cancellingNativeReader) Read(ctx context.Context, _ *types.ReadRequest) (*types.ReadResult, error) {
	s.cancel()
	if s.result != nil {
		return s.result, nil
	}
	return nil, ctx.Err()
}

func TestPaddleOCRVLNativeCancellationDoesNotFallBack(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, name := range []string{"failed probe", "accepted result after cancellation"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(500)
			}))
			defer server.Close()
			remote := cancellingNativeReader{cancel: cancel}
			if name == "accepted result after cancellation" {
				remote.result = &types.ReadResult{
					MarkdownContent: "Native output returned after the caller cancelled",
					Metadata: map[string]string{
						"pdf_native_only": "accepted", "image_source_type": "pdf_text_layer",
					},
				}
			}
			reader, err := (&paddleOCRVLEngine{}).NewReader(ctx, ReaderDeps{
				Overrides: map[string]string{"paddleocr_vl_endpoint": server.URL, "paddleocr_vl_pdf_mode": "auto"},
				Remote:    remote,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := reader.Read(ctx, &types.ReadRequest{
				FileName: "native.pdf", FileType: "pdf", FileContent: []byte("pdf"),
			})
			if !errors.Is(err, context.Canceled) || calls != 0 || result != nil {
				t.Fatalf("cancellation not propagated: %v, HTTP calls=%d, result=%+v", err, calls, result)
			}
		})
	}
}
