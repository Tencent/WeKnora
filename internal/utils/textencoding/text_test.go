package textencoding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

type shortReader struct {
	*bytes.Reader
	maxRead int
}

func (r shortReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		p = p[:r.maxRead]
	}
	return r.Reader.Read(p)
}

func TestReaderMatchesTextDecoding(t *testing.T) {
	encode := func(enc encoding.Encoding, value string) []byte {
		t.Helper()
		data, err := enc.NewEncoder().Bytes([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tests := [][]byte{
		nil,
		{},
		[]byte("a"),
		[]byte("UTF-8 中文 �"),
		[]byte("\xef\xbb\xbf正文"),
		encode(simplifiedchinese.GBK, "姓名,城市\n张三,北京\n"),
		encode(simplifiedchinese.GB18030, strings.Repeat("a", 128*1024)+"中文🍎"),
		encode(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), "中文🍎"),
		encode(unicode.UTF16(unicode.BigEndian, unicode.UseBOM), "中文🍎"),
		[]byte("\x93Quoted\x94 text \x96 costs 5 \x80."),
		[]byte("caf\xe9 \x81 menu"),
		{0xff, 0xfe, 0}, // Preserve the byte decoder's malformed UTF-16 policy too.
	}
	// Undefined legacy sequences can straddle either an input or decoder buffer.
	for _, size := range []int{4094, 4095, 4096, 32767, 32768} {
		tests = append(tests, append(bytes.Repeat([]byte{'a'}, size), 0x81))
	}
	for i, input := range tests {
		for _, chunk := range []int{1, 7, 32768} {
			source := shortReader{bytes.NewReader(input), chunk}
			reader, converted, err := NewReader(context.Background(), source)
			if err != nil {
				t.Fatalf("case %d chunk %d: %v", i, chunk, err)
			}
			got, err := io.ReadAll(reader)
			want := Decode(input)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("case %d chunk %d: decoding disagrees, err=%v", i, chunk, err)
			}
			if !converted && !bytes.Equal(got, input) {
				t.Fatalf("case %d: UTF-8 fast path changed bytes", i)
			}
		}
	}
}

type failingReader struct {
	*bytes.Reader
	err error
}

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type cancelingReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r cancelingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestReaderPropagatesIOAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := NewReader(ctx, bytes.NewReader([]byte("text"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reader: %v", err)
	}
	readErr := errors.New("storage read failed")
	broken := failingReader{bytes.NewReader(nil), readErr}
	if _, _, err := NewReader(context.Background(), broken); !errors.Is(err, readErr) {
		t.Fatalf("I/O error became an encoding fallback: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	canceling := cancelingReader{bytes.NewReader([]byte("text")), cancel}
	if _, _, err := NewReader(ctx, canceling); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during scan: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	reader, _, err := NewReader(ctx, bytes.NewReader([]byte{0xff, 0xfe, 'a', 0}))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during conversion: %v", err)
	}
}
