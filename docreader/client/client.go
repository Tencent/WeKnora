package client

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
)

func getMaxMessageSize() int {
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
	Logger.Printf("INFO: Creating new DocReader client connecting to %s", addr)

	maxMsgSize := getMaxMessageSize()
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgSize),
			grpc.MaxCallSendMsgSize(maxMsgSize),
		),
	}
	resolver.SetDefaultScheme("dns")

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
