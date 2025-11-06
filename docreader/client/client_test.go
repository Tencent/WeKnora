package client

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
)

func init() {
	// 配置测试日志
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("INFO: Initializing DocReader client tests")
}

func TestReadFromURL(t *testing.T) {
	log.Println("INFO: Starting TestReadFromURL")

	// 创建测试客户端
	log.Println("INFO: Creating test client")
	client, err := NewClient("localhost:50051")
	if err != nil {
		log.Printf("ERROR: Failed to create client: %v", err)
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// 启用调试日志
	client.SetDebug(true)

	// 测试 ReadFromURL 方法
	log.Println("INFO: Sending ReadFromURL request to server")
	startTime := time.Now()
	resp, err := client.ReadFromURL(
		context.Background(),
		&proto.ReadFromURLRequest{
			Url:   "https://example.com",
			Title: "test",
			ReadConfig: &proto.ReadConfig{
				ChunkSize:        512,
				ChunkOverlap:     50,
				Separators:       []string{"\n\n", "\n", "。"},
				EnableMultimodal: true,
			},
		},
	)

	requestDuration := time.Since(startTime)
	if err != nil {
		log.Printf("ERROR: ReadFromURL failed: %v", err)
		t.Fatalf("ReadFromURL failed: %v", err)
	}
	log.Printf("INFO: ReadFromURL completed in %v", requestDuration)

	// 验证结果
	chunkCount := len(resp.Chunks)
	log.Printf("INFO: Received %d chunks from URL parsing", chunkCount)
	if chunkCount == 0 {
		log.Println("WARN: Expected non-empty content but received none")
		t.Error("Expected non-empty content")
	}

	// 打印结果
	for i, chunk := range resp.Chunks {
		if i < 2 || i >= chunkCount-2 { // 只打印前两个和后两个块
			log.Printf("DEBUG: Chunk %d: %s", chunk.Seq, truncateString(chunk.Content, 50))
		} else if i == 2 && chunkCount > 4 {
			log.Printf("DEBUG: ... %d more chunks ...", chunkCount-4)
		}
	}

	log.Println("INFO: TestReadFromURL completed successfully")
}

func TestReadFromFileWithChunking(t *testing.T) {
	log.Println("INFO: Starting TestReadFromFileWithChunking")

	// 创建测试客户端
	log.Println("INFO: Creating test client")
	client, err := NewClient("localhost:50051")
	if err != nil {
		log.Printf("ERROR: Failed to create client: %v", err)
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// 启用调试日志
	client.SetDebug(true)

	// 读取测试文件
	log.Println("INFO: Reading test file")
	fileContent, err := os.ReadFile("../testdata/test.md")
	if err != nil {
		log.Printf("ERROR: Failed to read test file: %v", err)
		t.Fatalf("Failed to read test file: %v", err)
	}
	log.Printf("INFO: Read test file, size: %d bytes", len(fileContent))

	// 测试 ReadFromFile 方法，带分块参数
	log.Println("INFO: Sending ReadFromFile request to server")
	startTime := time.Now()
	resp, err := client.ReadFromFile(
		context.Background(),
		&proto.ReadFromFileRequest{
			FileContent: fileContent,
			FileName:    "test.md",
			FileType:    "md",
			ReadConfig: &proto.ReadConfig{
				ChunkSize:        200,
				ChunkOverlap:     50,
				Separators:       []string{"\n\n", "\n", "。"},
				EnableMultimodal: true,
			},
		},
	)

	requestDuration := time.Since(startTime)
	if err != nil {
		log.Printf("ERROR: ReadFromFile failed: %v", err)
		t.Fatalf("ReadFromFile failed: %v", err)
	}
	log.Printf("INFO: ReadFromFile completed in %v", requestDuration)

	// 验证结果
	chunkCount := len(resp.Chunks)
	log.Printf("INFO: Received %d chunks from file parsing", chunkCount)
	if chunkCount == 0 {
		log.Println("WARN: Expected non-empty content but received none")
		t.Error("Expected non-empty content")
	}

	// 打印结果
	for i, chunk := range resp.Chunks {
		if i < 2 || i >= chunkCount-2 { // 只打印前两个和后两个块
			log.Printf("DEBUG: Chunk %d: %s", chunk.Seq, truncateString(chunk.Content, 50))
		} else if i == 2 && chunkCount > 4 {
			log.Printf("DEBUG: ... %d more chunks ...", chunkCount-4)
		}
	}

	log.Println("INFO: TestReadFromFileWithChunking completed successfully")
}

// 截断字符串以供日志打印
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
