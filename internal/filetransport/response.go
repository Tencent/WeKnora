// Package filetransport streams already-authorized files. It performs no
// resource lookup or permission inference; callers own those boundaries.
package filetransport

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/utils/textencoding"
)

// Options defines how to serve an already-authorized reader.
type Options struct {
	Filename     string
	Download     bool
	ContentType  string
	Disposition  string
	CacheControl string
	Size         int64
	DecodeText   bool // Serve a UTF-8 representation of the preview.
}

// Serve closes reader on every path. Seekable backends support standard Range
// and HEAD via ServeContent; streaming-only backends return the full response
// without buffering the object just to implement seeking (RFC 9110 section 14.2).
func Serve(w http.ResponseWriter, r *http.Request, reader io.ReadCloser, options Options) error {
	defer func() { _ = reader.Close() }()
	var body io.Reader = reader
	if options.DecodeText {
		source, seekable := reader.(io.ReadSeeker)
		if !seekable {
			// Encoding detection reads the whole file. Spool streaming sources to
			// disk so detection can rewind without holding the file in memory.
			file, cleanup, err := spoolPreviewText(reader)
			if err != nil {
				return err
			}
			defer cleanup()
			source = file
		}
		decoded, converted, err := textencoding.NewReader(r.Context(), source)
		if err != nil {
			return err
		}
		if converted {
			options.Size = 0
		}
		switch {
		case !seekable:
			body = decoded
		case converted:
			// Range offsets for seekable sources refer to the UTF-8 response.
			file, cleanup, err := spoolPreviewText(decoded)
			if err != nil {
				return err
			}
			defer cleanup()
			body = file
		}
	}
	contentType, inline := secutils.SafeContentTypeByFilename(options.Filename)
	if options.Download {
		inline = false
	}
	if options.ContentType != "" {
		contentType = options.ContentType
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	if options.Disposition != "" {
		w.Header().Set("Content-Disposition", options.Disposition)
	} else if options.Filename != "" {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType(disposition,
				map[string]string{"filename": filepath.Base(options.Filename)}))
	} else {
		w.Header().Set("Content-Disposition", disposition)
	}
	if options.CacheControl != "" {
		w.Header().Set("Cache-Control", options.CacheControl)
	}
	if seeker, ok := body.(io.ReadSeeker); ok {
		http.ServeContent(w, r, options.Filename, time.Time{}, seeker)
		return nil
	}
	w.Header().Set("Accept-Ranges", "none")
	if options.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(options.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	_, err := io.Copy(w, body)
	return err
}

func spoolPreviewText(reader io.Reader) (*os.File, func(), error) {
	file, err := os.CreateTemp("", "weknora-text-preview-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	if _, err := io.Copy(file, reader); err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, err
	}
	return file, cleanup, nil
}
