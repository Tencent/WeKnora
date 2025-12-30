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

// getMaxMessageSize returns the maximum gRPC message size in bytes.
// Default is 50MB, can be configured via MAX_FILE_SIZE_MB environment variable.
func getMaxMessageSize() int {
	if sizeStr := os.Getenv("MAX_FILE_SIZE_MB"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil && size > 0 {
			return size * 1024 * 1024
		}
	}
	return 50 * 1024 * 1024 // default 50MB
}

// Logger is the default logger used by the client
var Logger = log.New(os.Stdout, "[DocReader] ", log.LstdFlags|log.Lmicroseconds)

// ImageInfo 表示一个图片的信息
type ImageInfo struct {
	URL         string // 图片URL（COS）
	Caption     string // 图片描述
	OCRText     string // OCR提取的文本
	OriginalURL string // 原始图片URL
	Start       int    // 图片在文本中的开始位置
	End         int    // 图片在文本中的结束位置
}

// Client represents a DocReader service client
type Client struct {
	conn *grpc.ClientConn
	proto.DocReaderClient
	debug bool
}

// NewClient creates a new DocReader client with the specified address
func NewClient(addr string) (*Client, error) {
	Logger.Printf("INFO: Creating new DocReader client connecting to %s", addr)

	// 设置消息大小限制 (configurable via GRPC_MAX_MESSAGE_SIZE_MB)
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

// Close closes the client connection
func (c *Client) Close() error {
	Logger.Printf("INFO: Closing DocReader client connection")
	return c.conn.Close()
}

// SetDebug enables or disables debug logging
func (c *Client) SetDebug(debug bool) {
	c.debug = debug
	Logger.Printf("INFO: Debug logging set to %v", debug)
}

// Log logs a message with the appropriate level
func (c *Client) Log(level string, format string, args ...interface{}) {
	if level == "DEBUG" && !c.debug {
		return
	}
	Logger.Printf("%s: %s", level, fmt.Sprintf(format, args...))
}

// GetImagesFromChunk 从一个Chunk中提取所有图片信息
func GetImagesFromChunk(chunk *proto.Chunk) []ImageInfo {
	if chunk == nil || len(chunk.Images) == 0 {
		return nil
	}

	images := make([]ImageInfo, 0, len(chunk.Images))
	for _, img := range chunk.Images {
		images = append(images, ImageInfo{
			URL:         img.Url,
			Caption:     img.Caption,
			OCRText:     img.OcrText,
			OriginalURL: img.OriginalUrl,
			Start:       int(img.Start),
			End:         int(img.End),
		})
	}

	return images
}

// HasImagesInChunk 判断一个Chunk是否包含图片
func HasImagesInChunk(chunk *proto.Chunk) bool {
	return chunk != nil && len(chunk.Images) > 0
}
