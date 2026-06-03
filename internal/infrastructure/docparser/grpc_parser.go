package docparser

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	docclient "github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
)

func getMaxMessageSize() int {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			return size * 1024 * 1024
		}
	}
	return 50 * 1024 * 1024
}

// GRPCDocumentReader implements DocumentReader over gRPC.
type GRPCDocumentReader struct {
	mu     sync.RWMutex
	conn   *grpc.ClientConn
	client proto.DocReaderClient
	addr   string
}

func NewGRPCDocumentReader(addr string) (*GRPCDocumentReader, error) {
	p := &GRPCDocumentReader{}
	if addr != "" {
		if err := p.connect(addr); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *GRPCDocumentReader) connect(addr string) error {
	authConfig := docclient.LoadAuthConfigFromEnv()
	opts, err := authConfig.BuildDialOptions(getMaxMessageSize())
	if err != nil {
		return fmt.Errorf("failed to build docreader dial options: %w", err)
	}
	if authConfig.TLSEnabled {
		logger.Infof(context.Background(), "TLS enabled for docreader gRPC client")
	}
	if authConfig.AuthToken != "" {
		logger.Infof(context.Background(),
			"Token authentication enabled for docreader gRPC client (TLS=%v)",
			authConfig.TLSEnabled,
		)
	}

	resolver.SetDefaultScheme("dns")

	start := time.Now()
	conn, err := grpc.Dial("dns:///"+addr, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to docreader: %w", err)
	}
	logger.Infof(context.Background(), "Connected to docreader in %v", time.Since(start))

	p.conn = conn
	p.client = proto.NewDocReaderClient(conn)
	p.addr = addr
	return nil
}

func (p *GRPCDocumentReader) Reconnect(addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
		p.client = nil
		p.addr = ""
	}
	return p.connect(addr)
}

func (p *GRPCDocumentReader) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.conn != nil
}

func (p *GRPCDocumentReader) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

var errNotConnected = fmt.Errorf("docreader service not connected")

func (p *GRPCDocumentReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, errNotConnected
	}

	protoReq := &proto.ReadRequest{
		FileContent: req.FileContent,
		FileName:    req.FileName,
		FileType:    req.FileType,
		Url:         req.URL,
		Title:       req.Title,
		RequestId:   req.RequestID,
		Config: &proto.ReadConfig{
			ParserEngine:          req.ParserEngine,
			ParserEngineOverrides: req.ParserEngineOverrides,
		},
	}

	// Use the streaming RPC so documents with many page images (large scanned
	// PDFs) are not capped by the unary message-size limit. The meta frame
	// arrives first, followed by one frame per image.
	result, err := p.readStream(ctx, client, protoReq)
	if err != nil {
		// An older docreader build may not implement ReadStream. Fall back to
		// the unary Read RPC so a version-skewed deployment still parses
		// documents (small/medium docs only — the unary path remains capped by
		// the gRPC message-size limit, which is exactly what streaming avoids).
		if status.Code(err) == codes.Unimplemented {
			logger.Warnf(ctx, "docreader ReadStream unimplemented, falling back to unary Read: %v", err)
			return p.readUnary(ctx, client, protoReq)
		}
		return nil, err
	}
	return result, nil
}

// readStream consumes the server-streaming ReadStream RPC: one meta frame
// followed by one frame per image. Errors are returned verbatim so the caller
// can inspect the gRPC status code (e.g. Unimplemented) for fallback.
func (p *GRPCDocumentReader) readStream(
	ctx context.Context, client proto.DocReaderClient, protoReq *proto.ReadRequest,
) (*types.ReadResult, error) {
	stream, err := client.ReadStream(ctx, protoReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC ReadStream failed: %w", err)
	}

	result := &types.ReadResult{}
	gotMeta := false
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return nil, fmt.Errorf("gRPC ReadStream recv failed: %w", recvErr)
		}

		if meta := frame.GetMeta(); meta != nil {
			gotMeta = true
			result.MarkdownContent = meta.GetMarkdownContent()
			result.ImageDirPath = meta.GetImageDirPath()
			result.Metadata = meta.GetMetadata()
			result.Error = meta.GetError()
			if n := meta.GetImageCount(); n > 0 {
				result.ImageRefs = make([]types.ImageRef, 0, n)
			}
			continue
		}

		if img := frame.GetImage(); img != nil {
			result.ImageRefs = append(result.ImageRefs, types.ImageRef{
				Filename:    img.GetFilename(),
				OriginalRef: img.GetOriginalRef(),
				MimeType:    img.GetMimeType(),
				StorageKey:  img.GetStorageKey(),
				ImageData:   img.GetImageData(),
			})
		}
	}

	if !gotMeta {
		return nil, fmt.Errorf("gRPC ReadStream returned no metadata frame")
	}
	return result, nil
}

// readUnary calls the legacy unary Read RPC. Used only as a compatibility
// fallback when the connected docreader does not implement ReadStream.
func (p *GRPCDocumentReader) readUnary(
	ctx context.Context, client proto.DocReaderClient, protoReq *proto.ReadRequest,
) (*types.ReadResult, error) {
	resp, err := client.Read(ctx, protoReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC Read failed: %w", err)
	}

	result := &types.ReadResult{
		MarkdownContent: resp.GetMarkdownContent(),
		ImageDirPath:    resp.GetImageDirPath(),
		Metadata:        resp.GetMetadata(),
		Error:           resp.GetError(),
	}
	if refs := resp.GetImageRefs(); len(refs) > 0 {
		result.ImageRefs = make([]types.ImageRef, 0, len(refs))
		for _, img := range refs {
			result.ImageRefs = append(result.ImageRefs, types.ImageRef{
				Filename:    img.GetFilename(),
				OriginalRef: img.GetOriginalRef(),
				MimeType:    img.GetMimeType(),
				StorageKey:  img.GetStorageKey(),
				ImageData:   img.GetImageData(),
			})
		}
	}
	return result, nil
}

func (p *GRPCDocumentReader) ListEngines(ctx context.Context, overrides map[string]string) ([]types.ParserEngineInfo, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, errNotConnected
	}

	resp, err := client.ListEngines(ctx, &proto.ListEnginesRequest{ConfigOverrides: overrides})
	if err != nil {
		return nil, fmt.Errorf("gRPC ListEngines failed: %w", err)
	}

	result := make([]types.ParserEngineInfo, 0, len(resp.GetEngines()))
	for _, e := range resp.GetEngines() {
		result = append(result, types.ParserEngineInfo{
			Name:              e.GetName(),
			Description:       e.GetDescription(),
			FileTypes:         e.GetFileTypes(),
			Available:         e.GetAvailable(),
			UnavailableReason: e.GetUnavailableReason(),
		})
	}
	return result, nil
}
