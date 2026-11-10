// Package filetransport streams already-authorized files. It performs no
// resource lookup or permission inference; callers own those boundaries.
package filetransport

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Options defines response metadata for an already-authorized reader.
type Options struct {
	Filename     string
	Download     bool
	ContentType  string
	Disposition  string
	CacheControl string
	Size         int64
}

// Serve closes reader on every path. Seekable backends support standard Range
// and HEAD via ServeContent; streaming-only backends return the full response
// without buffering the object just to implement seeking (RFC 9110 section 14.2).
func Serve(w http.ResponseWriter, r *http.Request, reader io.ReadCloser, options Options) error {
	defer func() { _ = reader.Close() }()
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
	if seeker, ok := reader.(io.ReadSeeker); ok {
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
	_, err := io.Copy(w, reader)
	return err
}
