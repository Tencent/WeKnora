package docparser

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	docclient "github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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
	return p.HealthCheck(context.Background()) == nil
}

func (p *GRPCDocumentReader) snapshot() (string, proto.DocReaderClient, *grpc.ClientConn) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.addr, p.client, p.conn
}

func (p *GRPCDocumentReader) HealthCheck(ctx context.Context) error {
	addr, _, conn := p.snapshot()
	if strings.TrimSpace(addr) == "" || conn == nil {
		return errNotConnected
	}

	healthCtx, cancel := withDefaultTimeout(ctx, docReaderHealthCheckTimeout)
	defer cancel()

	resp, err := healthpb.NewHealthClient(conn).Check(healthCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("docreader health check failed: %w", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("docreader health check status: %s", resp.GetStatus().String())
	}
	return nil
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
	addr, client, _ := p.snapshot()
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

	result, err := p.readWithClient(ctx, client, protoReq)
	if err != nil {
		if isRetryableGRPCError(err) {
			logger.Warnf(ctx, "docreader Read failed, reconnecting once: %v", err)
			retryClient, reconnectErr := p.reconnectForRetry(addr)
			if reconnectErr != nil {
				return nil, fmt.Errorf("%w (reconnect failed: %v)", err, reconnectErr)
			}
			return p.readWithClient(ctx, retryClient, protoReq)
		}
		return nil, err
	}
	return result, nil
}

func (p *GRPCDocumentReader) reconnectForRetry(addr string) (proto.DocReaderClient, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errNotConnected
	}
	if err := p.Reconnect(addr); err != nil {
		return nil, err
	}
	_, client, _ := p.snapshot()
	if client == nil {
		return nil, errNotConnected
	}
	return client, nil
}

func (p *GRPCDocumentReader) readWithClient(
	ctx context.Context, client proto.DocReaderClient, protoReq *proto.ReadRequest,
) (*types.ReadResult, error) {
	// 优先使用流式 RPC，避免图片较多的大文档受一元 RPC 消息大小限制；
	// 旧版 docreader 不支持流式接口时回退到一元 RPC。
	result, err := p.readStream(ctx, client, protoReq)
	if err != nil {
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
	addr, client, _ := p.snapshot()
	if client == nil {
		return nil, errNotConnected
	}

	resp, err := client.ListEngines(ctx, &proto.ListEnginesRequest{ConfigOverrides: overrides})
	if err != nil {
		if isRetryableGRPCError(err) {
			logger.Warnf(ctx, "docreader ListEngines failed, reconnecting once: %v", err)
			retryClient, reconnectErr := p.reconnectForRetry(addr)
			if reconnectErr != nil {
				return nil, fmt.Errorf("gRPC ListEngines failed: %w (reconnect failed: %v)", err, reconnectErr)
			}
			resp, err = retryClient.ListEngines(ctx, &proto.ListEnginesRequest{ConfigOverrides: overrides})
			if err == nil {
				return parserEngineInfoFromProto(resp), nil
			}
		}
		return nil, fmt.Errorf("gRPC ListEngines failed: %w", err)
	}

	return parserEngineInfoFromProto(resp), nil
}

func parserEngineInfoFromProto(resp *proto.ListEnginesResponse) []types.ParserEngineInfo {
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
	return result
}
