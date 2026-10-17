// Package video implements the video understanding client: a chat call whose
// user turn carries a whole video, plus the two ways local bytes reach that
// call.
//
// The wire shape is the OpenAI-compatible chat protocol with one extra content
// part:
//
//	{"role":"user","content":[
//	  {"type":"video_url","video_url":{"url":"oss://<key>"}},
//	  {"type":"text","text":"<prompt>"}]}
//
// The URL is one of three things. It can be an http(s) URL the vendor fetches
// itself; it can be an oss:// object staged by DashScope's temporary-file
// upload; or it can be a data: URL, the whole file base64 encoded into the
// request body. Which one is used is the knowledge base's choice
// (video_config.direct_input): "auto" picks inline for files that fit the
// vendor's inline ceiling and uploads the rest, "base64" never uploads,
// "upload" always stages the file, and "url" refuses local bytes because the
// URL must come from the caller.
//
// The upload exists because a model endpoint in the cloud cannot read a local
// file: staging is the documented way to hand DashScope a video, and a private
// deployment cannot serve a public URL either. Both the upload and the model
// call must use the same model and the same account: the key in the policy
// request is the key that later calls the model.
//
// The three-step upload is documented at
// https://help.aliyun.com/en/model-studio/get-temporary-file-url :
//
//  1. GET  {dashscope}/api/v1/uploads?action=getPolicy&model=<model> with the
//     API key, answering the OSS form policy plus the upload host;
//  2. POST {upload_host} as multipart/form-data carrying that policy and the
//     file (the file part must be last);
//  3. the object is then addressable as oss://{upload_dir}/{filename}.
//
// That URL scheme is only understood when the call opts in with the
// X-DashScope-OssResourceResolve: enable header; without it the endpoint treats
// oss:// as an unsupported absolute path.
package video

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
)

// Video defines the interface for video understanding models. It mirrors the
// VLM interface — one Predict call, model identity getters — with the input
// being one video rather than a list of images.
type Video interface {
	// Predict sends the video bytes and answers the prompt about them. The
	// filename supplies the object name, the extension the vendor stores and
	// the media type an inline request names; nothing is read from disk. How
	// the bytes travel is the client's transport setting (see
	// WithDirectInput).
	Predict(ctx context.Context, videoBytes []byte, fileName, prompt string) (string, error)

	// PredictURL answers a prompt about a video that is already reachable: an
	// oss:// object from an earlier upload, a public http(s) URL the vendor
	// can fetch on its own, or a data: URL. It is the second half of Predict
	// and the way a deployment reuses an object across calls.
	PredictURL(ctx context.Context, videoURL, prompt string) (string, error)

	GetModelName() string
	GetModelID() string
}

// directInputConfigurable is the transport switch a client may implement. It is
// an optional interface on purpose: the transport is a knowledge base setting,
// while a Video is built from a model row, which knows nothing about knowledge
// bases. A client that does not implement it keeps whichever transport it was
// built with, and WithDirectInput is what makes that explicit at the call site.
type directInputConfigurable interface {
	WithDirectInput(mode string) Video
}

// WithDirectInput returns client configured to carry videos the way mode asks
// (types.VideoDirectInputAuto / Base64 / URL / Upload). It is how
// video_config.direct_input reaches a client the model service built from the
// model row; a client without a transport switch is returned unchanged.
func WithDirectInput(client Video, mode string) Video {
	configurable, ok := client.(directInputConfigurable)
	if !ok {
		return client
	}
	return configurable.WithDirectInput(mode)
}

// Config holds the configuration needed to create a Video instance. It is
// built from a stored model row by ConfigFromModel, so the production path and
// the connection-test path share one mapping.
type Config struct {
	Source    types.ModelSource
	BaseURL   string
	ModelName string
	ModelID   string
	APIKey    string
	Provider  string
	Spec      *types.ModelSpecOverride
	// ExtraConfig and CustomHeaders travel with the chat call exactly like
	// they do for a chat row.
	ExtraConfig   map[string]string
	CustomHeaders map[string]string
	AppID         string
	AppSecret     string
	// UploadBaseURL overrides the temporary-file service root. Empty derives
	// it from the model's own DashScope host, falling back to the public
	// Beijing endpoint; VIDEO_UPLOAD_BASE_URL overrides both.
	UploadBaseURL string
	// Timeout bounds one upload and one model call. Zero takes the deployment
	// setting (VIDEO_HTTP_TIMEOUT_SECONDS, ten minutes by default).
	Timeout time.Duration
	// DirectInput is how Predict carries the video: empty or "auto" picks by
	// file size, "base64" always inlines, "upload" always stages the file and
	// "url" refuses local bytes. ConfigFromModel cannot fill it because it
	// belongs to the knowledge base, not the model row; the caller passes it
	// with WithDirectInput or through this field.
	DirectInput string
}

// ConfigFromModel builds a Config from a stored model row. appID / appSecret
// are the decrypted WeKnora Cloud credentials; callers resolve them.
func ConfigFromModel(m *types.Model, appID, appSecret string) *Config {
	if m == nil {
		return nil
	}
	return &Config{
		ModelID:       m.ID,
		APIKey:        m.Parameters.APIKey,
		BaseURL:       m.Parameters.BaseURL,
		ModelName:     m.Name,
		Source:        m.Source,
		Provider:      m.Parameters.Provider,
		Spec:          m.Parameters.Spec,
		ExtraConfig:   m.Parameters.ExtraConfig,
		CustomHeaders: m.Parameters.CustomHeaders,
		AppID:         appID,
		AppSecret:     appSecret,
	}
}

// ErrUnsupportedVendor is returned when the row names a vendor that does not
// serve video understanding in this build. Callers degrade on it like on any
// other client error.
var ErrUnsupportedVendor = errors.New("video understanding is not supported by this vendor")

// NewVideo resolves the catalog entry of the configured model and returns the
// video client. Resolution follows every other protocol client: the vendor's
// facts decide the protocol, the URL and the credential.
func NewVideo(config *Config) (Video, error) {
	if config == nil {
		return nil, errors.New("video config is nil")
	}
	if strings.TrimSpace(config.ModelName) == "" {
		return nil, errors.New("model name is required")
	}
	ref := modelruntime.Ref{
		Provider:  config.Provider,
		Model:     config.ModelName,
		BaseURL:   config.BaseURL,
		ModelType: types.ModelTypeVideo,
		Extra:     config.ExtraConfig,
		Override:  config.Spec,
	}
	resolved, err := modelruntime.Resolve(ref)
	if err != nil {
		return nil, err
	}
	// A vendor that does not declare video has not been checked for it. A row
	// that named the vendor is refused with a readable reason; a row that
	// named none and was matched by its URL is served as the OpenAI-compatible
	// baseline, which is what a gateway in front of DashScope is.
	if !resolved.Vendor.SupportsType(types.ModelTypeVideo) {
		if strings.TrimSpace(config.Provider) != "" {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedVendor, resolved.Vendor.Name)
		}
		ref.Provider = providers.GenericID
		resolved, err = modelruntime.Resolve(ref)
		if err != nil {
			return nil, err
		}
	}
	if err := validateVideoBaseURL(resolved.BaseURL); err != nil {
		return nil, err
	}

	endpoint, err := resolved.Endpoint(types.ModelTypeVideo, modelruntime.Connection{
		ModelID: config.ModelID,
		Credentials: api.Credentials{
			APIKey: config.APIKey, AppID: config.AppID, AppSecret: config.AppSecret,
		},
		Headers: config.CustomHeaders,
		Extra:   config.ExtraConfig,
	})
	if err != nil {
		return nil, err
	}
	if endpoint.URL != "" {
		if err := validateVideoBaseURL(endpoint.URL); err != nil {
			return nil, err
		}
	}

	return &dashscopeVideo{
		endpoint:    endpoint,
		chatURL:     endpoint.Resolve(chatCompletionsPath),
		uploadBase:  uploadBaseURL(config.UploadBaseURL, resolved.BaseURL),
		client:      newVideoHTTPClient(),
		timeout:     config.Timeout,
		modelName:   config.ModelName,
		modelID:     config.ModelID,
		provider:    resolved.Vendor.ID,
		directInput: types.NormalizeVideoDirectInput(config.DirectInput),
	}, nil
}

// WithDirectInput returns a copy of the client that carries videos the way mode
// asks. The receiver is left untouched: the model service hands the same client
// to whoever asks, and one knowledge base's transport must not leak into the
// next caller.
func (v *dashscopeVideo) WithDirectInput(mode string) Video {
	clone := *v
	clone.directInput = types.NormalizeVideoDirectInput(mode)
	return &clone
}
