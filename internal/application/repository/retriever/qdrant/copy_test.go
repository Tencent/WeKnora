package qdrant

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// copyTestRecordLimit bounds how many target chunk ids the fake keeps, so a run
// that writes the same page over and over cannot grow without limit.
const copyTestRecordLimit = 4096

// copyTestServer answers the RPCs CopyIndices makes and emulates the scroll
// cursor of a real Qdrant server: `offset` is inclusive (the returned page
// starts at that id), so the id the next request has to start at is the first
// point that was *not* returned - and the server hands that id back in
// next_page_offset.
type copyTestServer struct {
	mu      sync.Mutex
	source  []*qdrant.RetrievedPoint
	target  []string
	scrolls int
	upserts int
	written int
	// stuck answers every scroll with the same page and the same cursor, which
	// is what a server that never moves the cursor on looks like.
	stuck bool
}

func copyTestUUID(i int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
}

// copyTestVectors builds the dense vector a stored point carries.
func copyTestVectors() *qdrant.VectorsOutput {
	return &qdrant.VectorsOutput{
		VectorsOptions: &qdrant.VectorsOutput_Vector{
			Vector: &qdrant.VectorOutput{
				Vector: &qdrant.VectorOutput_Dense{
					Dense: &qdrant.DenseVector{Data: []float32{0.1, 0.2, 0.3, 0.4}},
				},
			},
		},
	}
}

func copyTestSourcePoint(i int) *qdrant.RetrievedPoint {
	chunk := fmt.Sprintf("chunk-%d", i)
	return &qdrant.RetrievedPoint{
		Id: qdrant.NewID(copyTestUUID(i)),
		Payload: newQdrantValueMap(map[string]any{
			fieldContent:         "content of " + chunk,
			fieldSourceID:        chunk,
			fieldSourceType:      int64(1),
			fieldChunkID:         chunk,
			fieldKnowledgeID:     "know-1",
			fieldKnowledgeBaseID: "kb-src",
			fieldTagID:           "",
			fieldIsEnabled:       true,
		}),
		Vectors: copyTestVectors(),
	}
}

// page answers one scroll the way the emulated server would.
func (s *copyTestServer) page(req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, *qdrant.PointId) {
	limit := int(req.GetLimit())
	if s.stuck {
		if limit > len(s.source) {
			limit = len(s.source)
		}
		return s.source[:limit], s.source[0].Id
	}

	start := 0
	if offset := req.GetOffset(); offset != nil {
		for start < len(s.source) && s.source[start].Id.GetUuid() < offset.GetUuid() {
			start++
		}
	}
	end := min(start+limit, len(s.source))
	if end >= len(s.source) {
		return s.source[start:end], nil
	}
	return s.source[start:end], s.source[end].Id
}

// repository returns a repository whose client is answered by this fake, plus
// the chunk mapping CopyIndices is called with for `count` source chunks.
func (s *copyTestServer) repository(t *testing.T, count int) (*qdrantRepository, map[string]string) {
	t.Helper()
	for i := range count {
		s.source = append(s.source, copyTestSourcePoint(i))
	}
	chunkMap := make(map[string]string, count)
	for i := range count {
		chunkMap[fmt.Sprintf("chunk-%d", i)] = fmt.Sprintf("target-chunk-%d", i)
	}

	client := newInterceptedQdrantClient(t, func(
		ctx context.Context, method string, req, reply any, _ *grpc.ClientConn,
		_ grpc.UnaryInvoker, _ ...grpc.CallOption,
	) error {
		// A live server stops answering once the caller's deadline passes.
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		switch request := req.(type) {
		case *qdrant.CollectionExistsRequest:
			reply.(*qdrant.CollectionExistsResponse).Result = &qdrant.CollectionExists{Exists: true}
			return nil
		case *qdrant.ScrollPoints:
			s.scrolls++
			response := reply.(*qdrant.ScrollResponse)
			response.Result, response.NextPageOffset = s.page(request)
			return nil
		case *qdrant.UpsertPoints:
			s.upserts++
			s.written += len(request.Points)
			for _, point := range request.Points {
				if len(s.target) >= copyTestRecordLimit {
					break
				}
				s.target = append(s.target, point.Payload[fieldChunkID].GetStringValue())
			}
			reply.(*qdrant.PointsOperationResponse).Result = &qdrant.UpdateResult{
				Status: qdrant.UpdateStatus_Completed,
			}
			return nil
		default:
			return fmt.Errorf("unexpected RPC %s", method)
		}
	})
	return &qdrantRepository{client: client, collectionBaseName: "Copy"}, chunkMap
}

func (s *copyTestServer) counts() (scrolls, upserts, written int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrolls, s.upserts, s.written
}

// repeatedTargetChunks returns the recorded target chunk ids that were written
// more than once, and how many extra copies they account for.
func (s *copyTestServer) repeatedTargetChunks() (chunks []string, extra int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[string]int, len(s.target))
	for _, chunk := range s.target {
		counts[chunk]++
	}
	for chunk, n := range counts {
		if n > 1 {
			chunks = append(chunks, chunk)
			extra += n - 1
		}
	}
	return chunks, extra
}

func (s *copyTestServer) summary() string {
	scrolls, upserts, written := s.counts()
	repeated, extra := s.repeatedTargetChunks()
	return fmt.Sprintf(
		"scrolls=%d upserts=%d points written=%d recorded=%d chunks written twice=%v duplicate copies=%d",
		scrolls, upserts, written, len(s.target), repeated, extra)
}

func runCopyTest(repo *qdrantRepository, chunkMap map[string]string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return repo.CopyIndices(ctx, "kb-src", map[string]string{"know-1": "know-2"},
		chunkMap, "kb-dst", 64, "doc")
}

// Qdrant's scroll offset is inclusive, so the last id of a page is not the
// cursor for the next page; the server returns that cursor in
// next_page_offset. Deriving the cursor from the page instead makes every page
// after the first re-read its last point and copy it into the target again.
func TestCopyIndicesCopiesEachSourceChunkOnce(t *testing.T) {
	const sourceCount = 150
	server := &copyTestServer{}
	repo, chunkMap := server.repository(t, sourceCount)
	defer func() { t.Log("copy run: " + server.summary()) }()

	if err := runCopyTest(repo, chunkMap, 30*time.Second); err != nil {
		t.Fatalf("CopyIndices: %v", err)
	}

	scrolls, upserts, written := server.counts()
	if repeated, extra := server.repeatedTargetChunks(); len(repeated) > 0 {
		t.Errorf("target chunks %v were copied again (%d extra copies)", repeated, extra)
	}
	// 150 chunks at 64 per page: 64 + 64 + 22, so three pages and three writes.
	if scrolls != 3 || upserts != 3 {
		t.Errorf("scrolls = %d, upserts = %d, want 3 and 3", scrolls, upserts)
	}
	if written != sourceCount {
		t.Errorf("target points = %d, want %d", written, sourceCount)
	}
}

// A source that never moves its cursor on must fail loudly instead of writing
// the same page into the target until the task deadline.
func TestCopyIndicesRejectsANonAdvancingCursor(t *testing.T) {
	const sourceCount = 64
	server := &copyTestServer{stuck: true}
	repo, chunkMap := server.repository(t, sourceCount)
	defer func() { t.Log("copy run: " + server.summary()) }()

	err := runCopyTest(repo, chunkMap, 5*time.Second)

	if err == nil || !strings.Contains(err.Error(), "made no progress") {
		t.Fatalf("CopyIndices error = %v, want one about a cursor that made no progress", err)
	}
	scrolls, upserts, written := server.counts()
	if scrolls != 2 {
		t.Errorf("scrolls = %d, want 2 (stop on the first repeated cursor)", scrolls)
	}
	if upserts != 1 || written != sourceCount {
		t.Errorf("upserts = %d writing %d points, want 1 writing %d", upserts, written, sourceCount)
	}
}
