// Package textencoding shares the text-upload decoding policy between parsing
// and data analysis. Encoding detection without a BOM is necessarily heuristic.
package textencoding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Match the simple reader's existing order: GB18030 includes GBK/GB2312;
// Windows-1252 handles Western Windows punctuation; Latin-1 is the fallback.
var legacyEncodings = []encoding.Encoding{simplifiedchinese.GB18030, charmap.Windows1252}

func utf16BOM(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff})
}

func utf16Encoding() encoding.Encoding {
	return unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)
}

// Decode returns text as UTF-8, preserving valid UTF-8 byte for byte, including
// its BOM. This is the simple reader's decoding policy, also used for CSVs.
func Decode(data []byte) []byte {
	if utf16BOM(data) {
		if text, err := utf16Encoding().NewDecoder().Bytes(data); err == nil {
			return text
		}
	}
	if utf8.Valid(data) {
		return data
	}
	for _, enc := range legacyEncodings {
		text, err := enc.NewDecoder().Bytes(data)
		if err == nil && !bytes.ContainsRune(text, utf8.RuneError) {
			return text
		}
	}
	text, _ := charmap.ISO8859_1.NewDecoder().Bytes(data)
	return text
}

// NewReader returns a UTF-8 reader and whether transcoding is needed. It checks
// the entire input, rather than guessing from a prefix, with bounded memory.
// source must remain open while the returned reader is consumed.
func NewReader(ctx context.Context, source io.ReadSeeker) (io.Reader, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	var head [2]byte
	n, err := io.ReadFull(source, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}
	reader := func(enc encoding.Encoding) (io.Reader, bool, error) {
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return nil, false, err
		}
		input := contextReader{ctx, source}
		if enc == nil {
			return input, false, nil
		}
		return transform.NewReader(input, enc.NewDecoder()), true, nil
	}
	if utf16BOM(head[:n]) {
		return reader(utf16Encoding())
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	_, err = io.Copy(io.Discard, transform.NewReader(contextReader{ctx, source}, encoding.UTF8Validator))
	if err == nil {
		return reader(nil)
	}
	if !errors.Is(err, encoding.ErrInvalidUTF8) {
		return nil, false, err
	}
	for _, enc := range legacyEncodings {
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return nil, false, err
		}
		decoder := transform.Chain(enc.NewDecoder(), replacementValidator{})
		_, err := io.Copy(io.Discard, transform.NewReader(contextReader{ctx, source}, decoder))
		if err == nil {
			return reader(enc)
		}
		if !errors.Is(err, errReplacement) {
			return nil, false, err
		}
	}
	return reader(charmap.ISO8859_1)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

var errReplacement = errors.New("text decoder produced a replacement character")

// x/text's legacy decoders replace undefined sequences instead of returning
// errors. Reject those candidates, including a replacement split over reads.
type replacementValidator struct{ transform.NopResetter }

func (replacementValidator) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, err error) {
	for nSrc < len(src) {
		if !atEOF && !utf8.FullRune(src[nSrc:]) {
			return nDst, nSrc, transform.ErrShortSrc
		}
		r, size := utf8.DecodeRune(src[nSrc:])
		if r == utf8.RuneError {
			return nDst, nSrc, errReplacement
		}
		if len(dst)-nDst < size {
			return nDst, nSrc, transform.ErrShortDst
		}
		copy(dst[nDst:], src[nSrc:nSrc+size])
		nDst += size
		nSrc += size
	}
	return nDst, nSrc, nil
}
