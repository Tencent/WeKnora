package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
)

// FormField is one text part of a multipart form. Order is kept.
type FormField struct {
	Name  string
	Value string
}

// FormFile is the file part of a multipart form.
type FormFile struct {
	Field    string
	FileName string
	Data     []byte
}

// EncodeMultipart writes fields and one file into a multipart/form-data body
// and returns it with its Content-Type. It is encoded once: those bytes are
// what a signing vendor hashes and what every retry sends again.
//
// The file part carries a filename and no Content-Type of its own. Servers
// identify the format from the extension then — OpenAI asks for "an
// extension-bearing filename", and vox-box (GPUStack's audio backend) falls
// back to guessing from it — whereas a guessed type that a server does not
// list is rejected outright.
func EncodeMultipart(fields []FormField, file FormFile) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`,
		file.Field, filepath.Base(file.FileName)))
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("encode form file: %w", err)
	}
	if _, err := part.Write(file.Data); err != nil {
		return nil, "", fmt.Errorf("encode form file: %w", err)
	}
	for _, f := range fields {
		if err := w.WriteField(f.Name, f.Value); err != nil {
			return nil, "", fmt.Errorf("encode form field %s: %w", f.Name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("encode form: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// PostMultipartWithRetry sends an encoded multipart body, retrying transport
// failures only, and decodes the JSON reply into out. It has the guarantees
// of PostJSONWithRetry.
func (e Endpoint) PostMultipartWithRetry(
	ctx context.Context, url string, body []byte, contentType string,
	out any, policy RetryPolicy, label string,
) error {
	if !strings.HasPrefix(contentType, "multipart/") {
		return fmt.Errorf("%s: not a multipart content type: %q", label, contentType)
	}
	return withRetry(ctx, policy, label, func() error {
		req, err := e.newPost(ctx, url, body, contentType)
		if err != nil {
			return err
		}
		return e.roundTrip(req, out)
	})
}
