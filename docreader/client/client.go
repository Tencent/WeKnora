package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func getMaxFileSize() int {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			return size * 1024 * 1024
		}
	}
	return 50 * 1024 * 1024
}

var Logger = log.New(os.Stdout, "[DocReader] ", log.LstdFlags|log.Lmicroseconds)

// ImageRefInfo represents an image reference from a converted document.
type ImageRefInfo struct {
	Filename    string
	OriginalRef string
	MimeType    string
	StorageKey  string
}

// Client represents a DocReader service client.
type Client struct {
	conn *grpc.ClientConn
	proto.DocReaderClient
	debug bool
}

func NewClient(addr string) (*Client, error) {
	authConfig := LoadAuthConfigFromEnv()
	return NewClientWithAuth(addr, authConfig)
}

func NewClientWithAuth(addr string, authConfig *AuthConfig) (*Client, error) {
	Logger.Printf("INFO: Creating new DocReader client connecting to %s", addr)

	// Reserve the same bounded envelope allowance as the App and Python server.
	// The raw file cap is checked separately in Read and ReadStream.
	maxMsgSize := getMaxFileSize() + (1 << 20)

	if authConfig == nil {
		authConfig = &AuthConfig{}
	}

	opts, err := authConfig.BuildDialOptions(maxMsgSize)
	if err != nil {
		Logger.Printf("ERROR: Failed to build dial options: %v", err)
		return nil, err
	}

	startTime := time.Now()
	conn, err := grpc.Dial("dns:///"+addr, opts...)
	if err != nil {
		Logger.Printf("ERROR: Failed to connect to DocReader service: %v", err)
		return nil, err
	}
	Logger.Printf("INFO: Successfully connected to DocReader service in %v", time.Since(startTime))

	return &Client{
		conn:            conn,
		DocReaderClient: proto.NewDocReaderClient(conn),
		debug:           false,
	}, nil
}

func validateFileContentSize(req *proto.ReadRequest) error {
	maxFileSize := getMaxFileSize()
	if size := len(req.GetFileContent()); size > maxFileSize {
		return status.Errorf(codes.ResourceExhausted,
			"docreader file content size %d exceeds maximum %d", size, maxFileSize)
	}
	return nil
}

// Read enforces the raw-file limit independently of protobuf framing.
func (c *Client) Read(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (*proto.ReadResponse, error) {
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	return c.DocReaderClient.Read(ctx, req, opts...)
}

// ReadStream applies the same file budget as Read, including on older servers.
func (c *Client) ReadStream(
	ctx context.Context, req *proto.ReadRequest, opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[proto.ReadStreamResponse], error) {
	if err := validateFileContentSize(req); err != nil {
		return nil, err
	}
	return c.DocReaderClient.ReadStream(ctx, req, opts...)
}

func (c *Client) Close() error {
	Logger.Printf("INFO: Closing DocReader client connection")
	return c.conn.Close()
}

func (c *Client) SetDebug(debug bool) {
	c.debug = debug
}

func (c *Client) Log(level string, format string, args ...interface{}) {
	if level == "DEBUG" && !c.debug {
		return
	}
	Logger.Printf("%s: %s", level, fmt.Sprintf(format, args...))
}

// GetImageRefsFromResponse extracts image references from a ReadResponse.
func GetImageRefsFromResponse(resp *proto.ReadResponse) []ImageRefInfo {
	if resp == nil || len(resp.ImageRefs) == 0 {
		return nil
	}

	refs := make([]ImageRefInfo, 0, len(resp.ImageRefs))
	for _, ref := range resp.ImageRefs {
		refs = append(refs, ImageRefInfo{
			Filename:    ref.Filename,
			OriginalRef: ref.OriginalRef,
			MimeType:    ref.MimeType,
			StorageKey:  ref.StorageKey,
		})
	}
	return refs
}
