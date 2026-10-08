package api

import (
	"bufio"
	"io"
	"strings"
)

// SSEEvent 表示一个 Server-Sent Events 事件
type SSEEvent struct {
	Data []byte
	Done bool
}

// SSEReader 用于读取 SSE 流
type SSEReader struct {
	scanner *bufio.Scanner
}

// NewSSEReader 创建 SSE 读取器
func NewSSEReader(reader io.Reader) *SSEReader {
	scanner := bufio.NewScanner(reader)
	// 设置更大的缓冲区以处理长行（思维链内容可能很长）
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	return &SSEReader{scanner: scanner}
}

// ReadEvent 读取下一个 SSE 事件
func (r *SSEReader) ReadEvent() (*SSEEvent, error) {
	for r.scanner.Scan() {
		line := r.scanner.Text()

		// 空行，跳过
		if line == "" {
			continue
		}

		// 解析 data 行。SSE 规范只要求 "data:" 前缀，冒号后的单个空格是可选的，
		// 所以两种写法都要接受。
		if !strings.HasPrefix(line, "data:") {
			// 其他行（如 event:, id: 等）跳过
			continue
		}
		payload := strings.TrimPrefix(line[len("data:"):], " ")

		// 检查是否为结束标记。个别网关写成 "data:[DONE]" 或在末尾留有空白，
		// 这里统一按去除首尾空白后的内容判断，避免把哨兵当成 JSON 去解析。
		if strings.TrimSpace(payload) == "[DONE]" {
			return &SSEEvent{Done: true}, nil
		}

		return &SSEEvent{Data: []byte(payload)}, nil

		// 其他行（如 event:, id: 等）跳过
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, io.EOF
}
