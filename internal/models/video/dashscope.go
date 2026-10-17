package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// chatCompletionsPath is the OpenAI-compatible endpoint the video call
	// goes to. The catalog's chat protocol uses the same path, and a vendor
	// that overrides its URL sets Endpoint.URL, which Resolve returns instead.
	chatCompletionsPath = "/chat/completions"
	// uploadsPath is DashScope's temporary-file service.
	// https://help.aliyun.com/en/model-studio/get-temporary-file-url
	uploadsPath = "/api/v1/uploads"
	// ossResolveHeader opts the model call into reading oss:// URLs. Without
	// it the endpoint rejects the scheme instead of fetching the object.
	ossResolveHeader = "X-DashScope-OssResourceResolve"
	// maxErrorBodyBytes bounds an upstream error read into the error message.
	maxErrorBodyBytes = 4 << 10
)

// dashscopeVideo implements Video against the OpenAI-compatible chat endpoint
// plus DashScope's temporary-file upload API.
type dashscopeVideo struct {
	endpoint   api.Endpoint
	chatURL    string
	uploadBase string
	client     *http.Client
	timeout    time.Duration
	modelName  string
	modelID    string
	// provider is the resolved vendor id. It decides the inline ceiling, and
	// it is the vendor's id rather than the model row's provider so a row that
	// named none but matched a DashScope URL is measured as DashScope.
	provider string
	// directInput is how Predict carries the video; see types.VideoConfig.
	// It is already normalized, so the switch below never sees "" or a typo.
	directInput string
}

// Predict hands the video to the model the way the configured transport asks:
// inline as a data: URL, staged in the vendor's temporary storage, or not at
// all when the operator committed to a caller-supplied URL. Every failure is
// returned as an error — a caller (the knowledge pipeline) degrades the video
// on it, and a silent empty answer would be indexed as content.
func (v *dashscopeVideo) Predict(
	ctx context.Context, videoBytes []byte, fileName, prompt string,
) (string, error) {
	if len(videoBytes) == 0 {
		return "", fmt.Errorf("video for model %s is empty", v.modelName)
	}
	if len(videoBytes) > MaxUploadBytes {
		return "", fmt.Errorf("video is %d bytes; the vendor accepts at most %d bytes per file",
			len(videoBytes), MaxUploadBytes)
	}
	// An explicit transport is a promise, not a preference: "base64" must not
	// upload when the file is too large (the operator may have chosen it to
	// keep the bytes out of the vendor's bucket), and "upload" must not go
	// inline because the file happened to be small.
	switch v.directInput {
	case types.VideoDirectInputURL:
		return "", fmt.Errorf(
			"video model %s is configured with direct_input %q, which needs a caller-supplied URL; use PredictURL",
			v.modelName, types.VideoDirectInputURL)
	case types.VideoDirectInputBase64:
		if err := v.checkInlineFits(len(videoBytes)); err != nil {
			return "", err
		}
		return v.predictInline(ctx, videoBytes, fileName, prompt)
	case types.VideoDirectInputUpload:
		return v.predictUpload(ctx, videoBytes, fileName, prompt)
	default: // auto: the file size decides
		if v.fitsInline(int64(len(videoBytes))) {
			return v.predictInline(ctx, videoBytes, fileName, prompt)
		}
		return v.predictUpload(ctx, videoBytes, fileName, prompt)
	}
}

// fitsInline reports whether a video of n bytes may travel inside the request.
func (v *dashscopeVideo) fitsInline(n int64) bool {
	return n <= inlineVideoLimit(v.provider)
}

// checkInlineFits refuses an oversized inline request with the numbers the
// operator needs. Neither transport falls back to the upload:
//
//   - "base64" exists precisely so the bytes are not staged in the vendor's
//     bucket; uploading after the promise failed would be the one outcome the
//     operator ruled out.
//   - "auto" does not promise "never upload", but it does promise a decision
//     made by file size. Retrying through the other transport would send the
//     file twice, add a slow upload and a second model call to a request that
//     is already failing, and hide the vendor's message behind a retry.
//   - A failed direct read already degrades to narration, frames or the
//     archive, so nothing is lost by surfacing the error, and the error names
//     the setting that fixes it.
func (v *dashscopeVideo) checkInlineFits(n int) error {
	limit := inlineVideoLimit(v.provider)
	if int64(n) <= limit {
		return nil
	}
	return fmt.Errorf(
		"video is %d bytes; model %s (provider %q) accepts at most %d bytes inline as base64: "+
			"set video_config.direct_input to %q to send large videos through the vendor's temporary storage",
		n, v.modelName, v.provider, limit, types.VideoDirectInputUpload)
}

// predictInline sends the video base64 encoded in the request body. No upload
// happens, so the bytes never reach the vendor's temporary bucket.
func (v *dashscopeVideo) predictInline(
	ctx context.Context, videoBytes []byte, fileName, prompt string,
) (string, error) {
	return v.PredictURL(ctx, inlineDataURL(videoBytes, fileName), prompt)
}

// predictUpload stages the bytes in the vendor's temporary storage and answers
// the prompt about the resulting object.
func (v *dashscopeVideo) predictUpload(
	ctx context.Context, videoBytes []byte, fileName, prompt string,
) (string, error) {
	ossURL, err := v.uploadTempFile(ctx, videoBytes, fileName)
	if err != nil {
		return "", fmt.Errorf("upload video for model %s: %w", v.modelName, err)
	}
	return v.PredictURL(ctx, ossURL, prompt)
}

// PredictURL sends the video reference in a chat message and returns the
// model's answer.
func (v *dashscopeVideo) PredictURL(ctx context.Context, videoURL, prompt string) (string, error) {
	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		return "", fmt.Errorf("video url sent to model %s is empty", v.modelName)
	}
	// The prompt is optional: an empty one asks the model to describe the
	// video, which is what the model test's video tab does when the operator
	// leaves the box blank. The knowledge pipeline always sends its built-in
	// prompt, so this only affects the diagnostic path.
	parts := []chatContentPart{{Type: "video_url", VideoURL: &chatVideoURL{URL: videoURL}}}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		parts = append(parts, chatContentPart{Type: "text", Text: prompt})
	}

	payload := chatRequest{
		Model:    v.endpoint.Model,
		Messages: []chatMessage{{Role: "user", Content: parts}},
	}

	// The header is what makes oss:// addressable; the caller's own headers
	// stay first so a configured header can never silently replace the
	// vendor's routing values. An inline data: URL is decoded from the body
	// and has nothing to resolve, so the header would only be noise there.
	endpoint := v.endpoint
	headers := make(map[string]string, len(endpoint.Headers)+1)
	for k, value := range endpoint.Headers {
		headers[k] = value
	}
	if !strings.HasPrefix(videoURL, inlineURLPrefix) {
		headers[ossResolveHeader] = "enable"
	}
	endpoint.Headers = headers

	callCtx, cancel := v.operationContext(ctx)
	defer cancel()
	var reply chatResponse
	if err := endpoint.PostJSON(callCtx, v.chatURL, payload, &reply); err != nil {
		return "", fmt.Errorf("video model call: %w", err)
	}
	if len(reply.Choices) == 0 {
		return "", fmt.Errorf("video model %s returned no choices", v.modelName)
	}
	content := strings.TrimSpace(reply.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("video model %s returned an empty answer", v.modelName)
	}
	if reply.Choices[0].FinishReason == finishReasonLength {
		// A partial answer is not a description of the video: indexing it as
		// the video's text would silently truncate the content, so the caller
		// degrades instead.
		return "", fmt.Errorf("video model %s exhausted its output budget (finish_reason=length)",
			v.modelName)
	}
	return content, nil
}

// GetModelName returns the configured model name.
func (v *dashscopeVideo) GetModelName() string { return v.modelName }

// GetModelID returns WeKnora's model row id.
func (v *dashscopeVideo) GetModelID() string { return v.modelID }

// uploadPolicy is the OSS form policy the temporary-file API answers with.
type uploadPolicy struct {
	Policy             string `json:"policy"`
	Signature          string `json:"signature"`
	UploadDir          string `json:"upload_dir"`
	UploadHost         string `json:"upload_host"`
	OSSAccessKeyID     string `json:"oss_access_key_id"`
	OSSObjectACL       string `json:"x_oss_object_acl"`
	OSSForbidOverwrite string `json:"x_oss_forbid_overwrite"`
	MaxFileSizeMB      int64  `json:"max_file_size_mb"`
	ExpireInSeconds    int64  `json:"expire_in_seconds"`
}

// uploadTempFile runs the three-step upload and returns the oss:// URL.
func (v *dashscopeVideo) uploadTempFile(
	ctx context.Context, videoBytes []byte, fileName string,
) (string, error) {
	policy, err := v.requestUploadPolicy(ctx)
	if err != nil {
		return "", err
	}
	if policy.MaxFileSizeMB > 0 && int64(len(videoBytes)) > policy.MaxFileSizeMB<<20 {
		return "", fmt.Errorf("video is %d bytes; the vendor accepts at most %d MB for model %s",
			len(videoBytes), policy.MaxFileSizeMB, v.modelName)
	}
	key := objectKey(policy.UploadDir, fileName)
	if err := v.putObject(ctx, policy, key, videoBytes, fileName); err != nil {
		return "", err
	}
	return ossURLPrefix + key, nil
}

// requestUploadPolicy asks DashScope for a one-shot upload policy. The model
// name ties the object to the same model the call will use; the vendor refuses
// a call that references an object uploaded for another model.
func (v *dashscopeVideo) requestUploadPolicy(ctx context.Context) (*uploadPolicy, error) {
	query := url.Values{}
	query.Set("action", "getPolicy")
	query.Set("model", v.endpoint.Model)
	target := strings.TrimRight(v.uploadBase, "/") + uploadsPath + "?" + query.Encode()

	callCtx, cancel := v.operationContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build upload policy request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// The policy request authenticates with the model's own key: the upload
	// and the call have to belong to the same account.
	v.endpoint.Auth(req, nil)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request upload policy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read upload policy: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("request upload policy: HTTP %d: %s",
			resp.StatusCode, errorBody(body))
	}
	var payload struct {
		Data    uploadPolicy `json:"data"`
		Code    string       `json:"code"`
		Message string       `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode upload policy: %w", err)
	}
	if payload.Data.UploadHost == "" || payload.Data.Policy == "" ||
		payload.Data.Signature == "" || payload.Data.OSSAccessKeyID == "" {
		return nil, fmt.Errorf("upload policy is incomplete: %s", errorBody(body))
	}
	return &payload.Data, nil
}

// putObject posts the multipart form the policy describes. The file part must
// be last: OSS stops reading the form at the file part, so any field after it
// is lost and the signature no longer matches what was uploaded.
func (v *dashscopeVideo) putObject(
	ctx context.Context, policy *uploadPolicy, key string, data []byte, fileName string,
) error {
	body, contentType, err := encodeUploadForm(policy, key, data, fileName)
	if err != nil {
		return err
	}
	if err := validateVideoBaseURL(policy.UploadHost); err != nil {
		// The host comes from the vendor's own answer, but a compromised or
		// compromised-looking reply must not be able to aim the upload at an
		// internal address.
		return fmt.Errorf("upload host: %w", err)
	}

	callCtx, cancel := v.operationContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, policy.UploadHost, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("send upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("send upload: HTTP %d: %s", resp.StatusCode, errorBody(reply))
	}
	return nil
}

// operationContext bounds one upload or one call. A parent that already set a
// shorter deadline keeps it: WithTimeout only ever shortens.
func (v *dashscopeVideo) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := v.timeout
	if timeout <= 0 {
		timeout = videoHTTPTimeout()
	}
	return context.WithTimeout(ctx, timeout)
}

// encodeUploadForm writes the policy fields and then the file. Field order is
// otherwise irrelevant; the file position is not (see putObject).
func encodeUploadForm(
	policy *uploadPolicy, key string, data []byte, fileName string,
) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fields := []struct{ name, value string }{
		{"OSSAccessKeyId", policy.OSSAccessKeyID},
		{"policy", policy.Policy},
		{"Signature", policy.Signature},
		{"key", key},
		{"x-oss-object-acl", policy.OSSObjectACL},
		{"x-oss-forbid-overwrite", policy.OSSForbidOverwrite},
		{"success_action_status", "200"},
	}
	for _, field := range fields {
		// An empty optional value (the ACL, the overwrite guard) is omitted
		// rather than sent blank, which OSS would reject.
		if field.value == "" && field.name != "key" && field.name != "success_action_status" {
			continue
		}
		if err := writer.WriteField(field.name, field.value); err != nil {
			return nil, "", fmt.Errorf("encode upload field %s: %w", field.name, err)
		}
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName))
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("encode upload file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("encode upload file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("encode upload form: %w", err)
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

// objectKey joins the policy's upload directory and the sanitized filename.
func objectKey(uploadDir, fileName string) string {
	dir := strings.Trim(uploadDir, "/")
	return strings.TrimSuffix(dir+"/"+sanitizeFileName(fileName), "/")
}

// sanitizeFileName keeps the extension and replaces everything the object key
// cannot carry safely. A stored filename is user-controlled, so a slash, a
// space or a non-ASCII name must not change the key's shape; the extension is
// what the vendor uses to recognise the container.
//
// A name that had to be changed also gets a short digest of the original,
// because the replacement is lossy: two Chinese-named recordings of the same
// length would otherwise collapse onto one object key, and OSS is configured
// here to refuse an overwrite, which would fail the second upload.
func sanitizeFileName(fileName string) string {
	base := path.Base(strings.ReplaceAll(strings.TrimSpace(fileName), "\\", "/"))
	ext := objectKeySafeExtension(path.Ext(base))
	stem := strings.TrimSuffix(base, ext)
	var b strings.Builder
	replaced := false
	for _, r := range stem {
		if objectKeySafe(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
		replaced = true
	}
	cleaned := strings.Trim(b.String(), ".-")
	if cleaned == "" {
		cleaned = "video"
	}
	if len(cleaned) > 100 {
		cleaned = cleaned[:100]
	}
	if replaced {
		cleaned += "-" + shortDigest(fileName)
	}
	return cleaned + ext
}

// objectKeySafe reports whether one rune may appear in an object key without
// escaping. The set is deliberately narrow: OSS and the vendor both accept
// more, but a key that needs escaping is a key somebody will eventually
// mis-encode.
func objectKeySafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.':
		return true
	default:
		return false
	}
}

// objectKeySafeExtension returns the extension when every character in it is
// safe, and "" otherwise. The stem was trimmed against the raw extension, so
// the caller must pass the same value it trims.
func objectKeySafeExtension(ext string) string {
	if len(ext) < 2 {
		return ""
	}
	for _, r := range ext[1:] {
		if !objectKeySafe(r) {
			return ""
		}
	}
	return ext
}

// shortDigest is the collision breaker for sanitized names.
func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}

// errorBody flattens an upstream error body into one readable line.
func errorBody(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return "(empty body)"
	}
	if len(text) > maxErrorBodyBytes {
		text = text[:maxErrorBodyBytes] + "…"
	}
	return text
}
