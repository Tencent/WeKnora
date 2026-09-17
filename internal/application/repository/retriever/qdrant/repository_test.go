package qdrant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

func TestNewQdrantValueMapSanitizesInvalidUTF8AndNUL(t *testing.T) {
	malformed := "prefix" + string([]byte{0xff}) + "\x00suffix"
	payload := newQdrantValueMap(map[string]any{
		fieldContent:    malformed,
		fieldSourceType: int64(1),
		fieldIsEnabled:  true,
	})

	got := payload[fieldContent].GetStringValue()
	if got != "prefixsuffix" {
		t.Fatalf("unexpected sanitized content: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("sanitized content is not valid UTF-8: % x", []byte(got))
	}
	if gotSourceType := payload[fieldSourceType].GetIntegerValue(); gotSourceType != 1 {
		t.Fatalf("source type changed: got %d, want 1", gotSourceType)
	}
	if gotEnabled := payload[fieldIsEnabled].GetBoolValue(); !gotEnabled {
		t.Fatal("is_enabled changed during payload sanitization")
	}
}

func TestNewQdrantValueMapPreservesValidUTF8(t *testing.T) {
	valid := "valid UTF-8 中文内容"
	payload := newQdrantValueMap(map[string]any{
		fieldContent: valid,
	})

	got := payload[fieldContent].GetStringValue()
	if got != valid {
		t.Fatalf("valid content changed: got %q, want %q", got, valid)
	}
}

func TestCreatePayloadSanitizesAllStringFields(t *testing.T) {
	malformed := "a" + string([]byte{0xff}) + "\x00b"
	embedding := &QdrantVectorEmbedding{
		Content:         malformed,
		SourceID:        malformed,
		SourceType:      2,
		ChunkID:         malformed,
		KnowledgeID:     malformed,
		KnowledgeBaseID: malformed,
		TagID:           malformed,
		IsEnabled:       true,
	}

	payload := createPayload(embedding)
	stringFields := []string{
		fieldContent,
		fieldSourceID,
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldTagID,
	}
	for _, field := range stringFields {
		got := payload[field].GetStringValue()
		if got != "ab" {
			t.Errorf("%s was not sanitized correctly: got %q", field, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s remains invalid UTF-8: % x", field, []byte(got))
		}
	}
	if got := payload[fieldSourceType].GetIntegerValue(); got != 2 {
		t.Errorf("source type changed: got %d, want 2", got)
	}
	if got := payload[fieldIsEnabled].GetBoolValue(); !got {
		t.Error("is_enabled changed during payload creation")
	}
}

// Intercept the SDK's RPCs so failures and cancellation need no live server.
func newPayloadUpdateTestRepository(t *testing.T, collections []string, listErr error,
	setPayload func(context.Context, *qdrant.SetPayloadPoints) error,
) *qdrantRepository {
	t.Helper()
	client, err := qdrant.NewClient(&qdrant.Config{
		Host:                   "localhost",
		PoolSize:               1,
		SkipCompatibilityCheck: true,
		GrpcOptions: []grpc.DialOption{grpc.WithUnaryInterceptor(func(
			ctx context.Context, method string, req, reply any, _ *grpc.ClientConn,
			_ grpc.UnaryInvoker, _ ...grpc.CallOption,
		) error {
			switch request := req.(type) {
			case *qdrant.ListCollectionsRequest:
				if listErr != nil {
					return listErr
				}
				response := reply.(*qdrant.ListCollectionsResponse)
				for _, name := range collections {
					response.Collections = append(response.Collections, &qdrant.CollectionDescription{Name: name})
				}
				return nil
			case *qdrant.SetPayloadPoints:
				return setPayload(ctx, request)
			default:
				return fmt.Errorf("unexpected RPC %s", method)
			}
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return &qdrantRepository{client: client, collectionBaseName: "vectors"}
}

func TestBatchPayloadUpdates(t *testing.T) {
	operations := []struct {
		name   string
		groups int
		run    func(context.Context, *qdrantRepository) error
	}{
		{"enable", 1, func(ctx context.Context, repo *qdrantRepository) error {
			return repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"chunk": true})
		}},
		{"disable", 1, func(ctx context.Context, repo *qdrantRepository) error {
			return repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"chunk": false})
		}},
		{"mixed status", 2, func(ctx context.Context, repo *qdrantRepository) error {
			return repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"on": true, "off": false})
		}},
		{"tags", 2, func(ctx context.Context, repo *qdrantRepository) error {
			return repo.BatchUpdateChunkTagID(ctx, map[string]string{"one": "tag-a", "two": "tag-b"})
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			modes := []string{"success", "partial failure", "all failures", "list failure", "no collections"}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					collections := []string{"vectors_768", "unrelated_768", "vectors_1536"}
					if mode == "no collections" {
						collections = []string{"unrelated_768"}
					}
					var listErr error
					if mode == "list failure" {
						listErr = errors.New("list unavailable")
					}
					calls := 0
					var failures []error
					var descriptions []string
					repo := newPayloadUpdateTestRepository(t, collections, listErr,
						func(_ context.Context, request *qdrant.SetPayloadPoints) error {
							calls++
							if request.CollectionName == "unrelated_768" {
								t.Error("updated an unrelated collection")
							}
							fail := mode == "all failures" ||
								(mode == "partial failure" && request.CollectionName == "vectors_768")
							if !fail {
								return nil
							}
							failure := fmt.Errorf("failure %d", calls)
							failures = append(failures, failure)
							description := fmt.Sprintf("set chunk tag_id %q",
								request.Payload[fieldTagID].GetStringValue())
							if enabled, ok := request.Payload[fieldIsEnabled]; ok {
								description = "disable chunks"
								if enabled.GetBoolValue() {
									description = "enable chunks"
								}
							}
							descriptions = append(descriptions, description+" in collection "+request.CollectionName)
							return failure
						})
					err := operation.run(context.Background(), repo)
					wantCalls := 2 * operation.groups
					if mode == "list failure" || mode == "no collections" {
						wantCalls = 0
					}
					if calls != wantCalls {
						t.Fatalf("got %d updates, want %d", calls, wantCalls)
					}
					if listErr != nil && !errors.Is(err, listErr) {
						t.Fatalf("lost list failure: %v", err)
					}
					if listErr == nil && len(failures) == 0 && err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					for i, failure := range failures {
						if !errors.Is(err, failure) {
							t.Errorf("lost failure %v in %v", failure, err)
						}
						if err == nil || !strings.Contains(err.Error(), descriptions[i]) {
							t.Errorf("missing operation/collection %q in %v", descriptions[i], err)
						}
					}
				})
			}

			t.Run("cancellation retains earlier failures", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failure := errors.New("first update failed")
				calls := 0
				repo := newPayloadUpdateTestRepository(t, []string{"vectors_768", "vectors_1536", "vectors_384"}, nil,
					func(context.Context, *qdrant.SetPayloadPoints) error {
						calls++
						if calls == 1 {
							return failure
						}
						cancel()
						return ctx.Err()
					})
				err := operation.run(ctx, repo)
				if calls != 2 || !errors.Is(err, failure) || !errors.Is(err, context.Canceled) {
					t.Fatalf("calls=%d, err=%v; want 2 calls and both errors", calls, err)
				}
			})

			t.Run("canceled before starting", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := operation.run(ctx, &qdrantRepository{}); !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation before any RPC, got %v", err)
				}
			})
			t.Run("canceled during final update", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				repo := newPayloadUpdateTestRepository(t, []string{"vectors_768"}, nil,
					func(context.Context, *qdrant.SetPayloadPoints) error {
						calls++
						if calls == operation.groups {
							cancel()
						}
						return nil
					})
				if err := operation.run(ctx, repo); !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation instead of success, got %v", err)
				}
			})
			t.Run("expired deadline", func(t *testing.T) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				if err := operation.run(ctx, &qdrantRepository{}); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected deadline error before any RPC, got %v", err)
				}
			})
		})
	}
}

func TestBatchPayloadUpdatesEmptyInput(t *testing.T) {
	repo := &qdrantRepository{}
	if err := repo.BatchUpdateChunkEnabledStatus(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.BatchUpdateChunkTagID(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
