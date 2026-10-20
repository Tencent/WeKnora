package weaviate

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeWeaviate stands in for a Weaviate node: an HTTP server for the REST API
// and a gRPC server for search, batch insert and batch delete, which is how the
// v6 client splits its requests. Each handler is optional; a request nobody
// handles fails the test.
type fakeWeaviate struct {
	pb.UnimplementedWeaviateServer

	rest         http.HandlerFunc
	search       func(*pb.SearchRequest) (*pb.SearchReply, error)
	batchObjects func(*pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error)
	batchDelete  func(*pb.BatchDeleteRequest) (*pb.BatchDeleteReply, error)

	// version is what /v1/meta reports; empty means a current server.
	version string
	// metaFailures is how many /v1/meta requests are answered with 503 first,
	// the way a server that is still starting answers.
	metaFailures int

	mu          sync.Mutex
	restHeaders []http.Header
	grpcHeaders []metadata.MD

	restAddress string
	grpcAddress string
}

// start serves f on loopback until the test ends.
func (f *fakeWeaviate) start(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isMeta := r.Method == http.MethodGet && r.URL.Path == "/v1/meta"
		f.mu.Lock()
		f.restHeaders = append(f.restHeaders, r.Header.Clone())
		unavailable := isMeta && f.metaFailures > 0
		if unavailable {
			f.metaFailures--
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// The client reads /v1/meta while it connects.
		if unavailable {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if isMeta {
			version := f.version
			if version == "" {
				version = "1.40.5"
			}
			_, _ = fmt.Fprintf(w, `{"version":%q,"grpcMaxMessageSize":104858000}`, version)
			return
		}
		if f.rest == nil {
			t.Errorf("unexpected REST request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.rest(w, r)
	}))
	t.Cleanup(server.Close)
	f.restAddress = strings.TrimPrefix(server.URL, "http://")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any,
		_ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		f.mu.Lock()
		f.grpcHeaders = append(f.grpcHeaders, md.Copy())
		f.mu.Unlock()
		return handler(ctx, req)
	}))
	pb.RegisterWeaviateServer(grpcServer, f)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	f.grpcAddress = listener.Addr().String()
}

// repository starts f and returns a repository connected to it.
func (f *fakeWeaviate) repository(t *testing.T, baseName string) *weaviateRepository {
	t.Helper()
	f.start(t)
	client, err := NewClient(context.Background(), ClientConfig{
		Host:        f.restAddress,
		GrpcAddress: f.grpcAddress,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return &weaviateRepository{client: client, collectionBaseName: baseName}
}

func (f *fakeWeaviate) Search(_ context.Context, req *pb.SearchRequest) (*pb.SearchReply, error) {
	if f.search == nil {
		return f.UnimplementedWeaviateServer.Search(context.Background(), req)
	}
	return f.search(req)
}

func (f *fakeWeaviate) BatchObjects(_ context.Context, req *pb.BatchObjectsRequest) (*pb.BatchObjectsReply, error) {
	if f.batchObjects == nil {
		return f.UnimplementedWeaviateServer.BatchObjects(context.Background(), req)
	}
	return f.batchObjects(req)
}

func (f *fakeWeaviate) BatchDelete(_ context.Context, req *pb.BatchDeleteRequest) (*pb.BatchDeleteReply, error) {
	if f.batchDelete == nil {
		return f.UnimplementedWeaviateServer.BatchDelete(context.Background(), req)
	}
	return f.batchDelete(req)
}

// fakeRow is one object a fake search answers with.
type fakeRow struct {
	id         string
	properties map[string]any
	vector     []float32
	certainty  *float32
	score      *float32
}

func searchReply(rows ...fakeRow) *pb.SearchReply {
	reply := &pb.SearchReply{}
	for _, row := range rows {
		id := uuid.MustParse(row.id)
		meta := &pb.MetadataResult{Id: row.id, IdAsBytes: id[:]}
		if row.vector != nil {
			meta.Vectors = []*pb.Vectors{{
				Name:        fieldEmbedding,
				VectorBytes: float32Bytes(row.vector),
				Type:        pb.Vectors_VECTOR_TYPE_SINGLE_FP32,
			}}
		}
		if row.certainty != nil {
			meta.Certainty, meta.CertaintyPresent = *row.certainty, true
		}
		if row.score != nil {
			meta.Score, meta.ScorePresent = *row.score, true
		}
		fields := make(map[string]*pb.Value, len(row.properties))
		for name, value := range row.properties {
			fields[name] = protoValue(value)
		}
		reply.Results = append(reply.Results, &pb.SearchResult{
			Properties: &pb.PropertiesResult{NonRefProps: &pb.Properties{Fields: fields}},
			Metadata:   meta,
		})
	}
	return reply
}

func protoValue(value any) *pb.Value {
	switch v := value.(type) {
	case string:
		return &pb.Value{Kind: &pb.Value_TextValue{TextValue: v}}
	case int:
		return &pb.Value{Kind: &pb.Value_IntValue{IntValue: int64(v)}}
	case bool:
		return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: v}}
	}
	panic("unsupported fake property value")
}

func float32Bytes(vector []float32) []byte {
	b := make([]byte, 4*len(vector))
	for i, f := range vector {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

// filterCondition finds the condition on property in a filter tree.
func filterCondition(f *pb.Filters, property string) *pb.Filters {
	if f == nil {
		return nil
	}
	if f.GetTarget().GetProperty() == property {
		return f
	}
	for _, sub := range f.GetFilters() {
		if found := filterCondition(sub, property); found != nil {
			return found
		}
	}
	return nil
}

// filterTexts returns the text values a condition compares against.
func filterTexts(f *pb.Filters) []string {
	if f == nil {
		return nil
	}
	if values := f.GetValueTextArray().GetValues(); values != nil {
		return values
	}
	if f.GetTestValue() != nil {
		if text, ok := f.GetTestValue().(*pb.Filters_ValueText); ok {
			return []string{text.ValueText}
		}
	}
	return nil
}

func float32Ptr(v float32) *float32 { return &v }
