package chat

import (
	"bufio"
	"errors"
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

		// 检查是否为结束标记
		if line == "data: [DONE]" {
			return &SSEEvent{Done: true}, nil
		}

		// 解析 data 行
		if strings.HasPrefix(line, "data: ") {
			jsonStr := line[6:]
			return &SSEEvent{Data: []byte(jsonStr)}, nil
		}

		// 其他行（如 event:, id: 等）跳过
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, errors.New("EOF")
}
