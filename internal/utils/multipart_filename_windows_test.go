package utils

import (
	"bytes"
	"mime/multipart"
	"net/textproto"
	"testing"
)

func TestRestoreMultipartFilenameAfterWindowsParse(t *testing.T) {
	raw := string([]byte{0xc7, 0xb0, 0x81, 0x5c, 0xba, 0xf3, '.', 'p', 'd', 'f'})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + raw + `"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("file contents")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary()).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = form.RemoveAll() })
	file := form.File["file"][0]
	if file.Filename != string([]byte{0xba, 0xf3, '.', 'p', 'd', 'f'}) {
		t.Fatalf("expected Windows parser truncation, got %x", []byte(file.Filename))
	}
	RestoreMultipartFilename(file)
	if file.Filename != "前乗后.pdf" {
		t.Fatalf("restored filename = %q", file.Filename)
	}
}
