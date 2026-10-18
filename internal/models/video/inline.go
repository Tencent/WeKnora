package video

import (
	"encoding/base64"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/providers"
)

// Inline video input: the file travels base64 encoded inside one chat request
// instead of being staged in the vendor's temporary storage first.
//
// The vendor caps the *string* it will parse, not the file, so each ceiling
// below is the documented request limit converted back into video bytes. Base64
// is four characters per three bytes, and the data-URL prefix plus the JSON
// around it are charged to the same body:
//
//   - DashScope rejects any single string longer than 28,000,000 characters
//     ("String value length (28049408) exceeds the maximum allowed (28000000,
//     from StreamReadConstraints.getMaxStringLength())"). Inline was measured
//     to work for a 1.6 MB video and to fail for a 31.7 MB one, whose base64 is
//     42.3 MB; 28,000,000 characters carry at most 21 MB of video.
//   - Tencent TokenHub caps the request body at 100 MB, so the video may take
//     three quarters of it. Some of its models do not accept base64 at all
//     (HY-Vision-Video, YT-VITA, GLM-5V-Turbo); those need "upload".
//   - Any other provider gets a deliberately conservative 20 MB, because a
//     gateway's body limit is not something this build can read.
const (
	// dashscopeInlineStringLimit is the longest single string DashScope's
	// request parser accepts, per its own error message.
	dashscopeInlineStringLimit int64 = 28_000_000
	// tencentInlineBodyLimit is Tencent TokenHub's documented request-body
	// ceiling.
	tencentInlineBodyLimit int64 = 100_000_000
	// defaultInlineVideoBytes is how much video a provider with no known
	// ceiling may send inline.
	defaultInlineVideoBytes int64 = 20_000_000
	// inlineDataURLOverhead covers the "data:video/x-matroska;base64," prefix
	// (30 bytes for the longest media type this package emits) plus slack for
	// the JSON around the string, which the body limit counts too.
	inlineDataURLOverhead int64 = 32
	// inlineURLPrefix marks a request whose video travels in the body rather
	// than as a reference the vendor fetches.
	inlineURLPrefix = "data:"
	// defaultInlineMIME is what an unrecognised extension is sent as. A stored
	// video is an mp4 far more often than it is anything else.
	defaultInlineMIME = "video/mp4"
)

// inlineVideoMIMETypes maps the extensions a stored video can carry to the
// media type its data URL has to name. The vendor picks its decoder from the
// media type, so sending a QuickTime file as video/mp4 is a silent mismatch.
var inlineVideoMIMETypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/x-m4v",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".flv":  "video/x-flv",
	".wmv":  "video/x-ms-wmv",
	".mpeg": "video/mpeg",
	".mpg":  "video/mpeg",
	".ts":   "video/mp2t",
	".3gp":  "video/3gpp",
}

// inlineVideoLimit reports the largest video that may be sent base64 for this
// model's provider, derived from the vendor's documented request limits.
// provider is the resolved vendor id (models.parameters.provider), not the
// model name: two models of one vendor share the request cap, and a model that
// rejects base64 altogether is the operator's cue to choose "upload", not a
// reason to guess a different number here.
func inlineVideoLimit(provider string) int64 {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case providers.AliyunID, "dashscope":
		return inlineBytesFor(dashscopeInlineStringLimit)
	case "tencent", providers.HunyuanID:
		return inlineBytesFor(tencentInlineBodyLimit)
	default:
		return defaultInlineVideoBytes
	}
}

// inlineBytesFor converts a request-body budget in characters into the number
// of video bytes that base64 and the data-URL prefix turn into it. It rounds
// down: one byte past the ceiling is a rejected call, not a truncated one.
func inlineBytesFor(budget int64) int64 {
	if budget <= inlineDataURLOverhead {
		return 0
	}
	return (budget - inlineDataURLOverhead) / 4 * 3
}

// inlineVideoMIMEType derives the data URL's media type from the filename. A
// name with no extension or one this build does not know is sent as video/mp4,
// the container every supported vendor accepts.
func inlineVideoMIMEType(fileName string) string {
	ext := strings.ToLower(path.Ext(strings.ReplaceAll(strings.TrimSpace(fileName), "\\", "/")))
	if mimeType, ok := inlineVideoMIMETypes[ext]; ok {
		return mimeType
	}
	return defaultInlineMIME
}

// inlineDataURL wraps the bytes as the data: URL the chat content part carries.
// The encoded string is sized once: a video at the inline ceiling is tens of
// megabytes, and string concatenation would copy it again.
func inlineDataURL(videoBytes []byte, fileName string) string {
	encoded := base64.StdEncoding.EncodeToString(videoBytes)
	prefix := inlineURLPrefix + inlineVideoMIMEType(fileName) + ";base64,"

	var url strings.Builder
	url.Grow(len(prefix) + len(encoded))
	url.WriteString(prefix)
	url.WriteString(encoded)
	return url.String()
}
