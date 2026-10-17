// Package video implements the video understanding client: a chat call whose
// user turn carries a whole video, plus the vendor's temporary-file upload that
// turns local bytes into the URL the call needs.
//
// The wire shape is the OpenAI-compatible chat protocol with one extra content
// part:
//
//	{"role":"user","content":[
//	  {"type":"video_url","video_url":{"url":"oss://<key>"}},
//	  {"type":"text","text":"<prompt>"}]}
//
// DashScope will not fetch a local file and a private deployment cannot serve a
// public URL, so the bytes are uploaded first and referenced by the resulting
// oss:// URL (valid for 48 hours). That URL scheme is only understood when the
// call opts in with the X-DashScope-OssResourceResolve: enable header; without
// it the endpoint treats oss:// as an unsupported absolute path.
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
// The upload and the model call must use the same model and the same account:
// the key in the policy request is the key that later calls the model.
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
	// Predict uploads the video bytes and answers the prompt about them. The
	// filename supplies the object name and the extension the vendor stores;
	// nothing is read from disk.
	Predict(ctx context.Context, videoBytes []byte, fileName, prompt string) (string, error)

	// PredictURL answers a prompt about a video that is already reachable: an
	// oss:// object from an earlier upload, or a public http(s) URL the vendor
	// can fetch on its own. It is the second half of Predict and the way a
	// deployment reuses an object across calls.
	PredictURL(ctx context.Context, videoURL, prompt string) (string, error)

	GetModelName() string
	GetModelID() string
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
		endpoint:   endpoint,
		chatURL:    endpoint.Resolve(chatCompletionsPath),
		uploadBase: uploadBaseURL(config.UploadBaseURL, resolved.BaseURL),
		client:     newVideoHTTPClient(),
		timeout:    config.Timeout,
		modelName:  config.ModelName,
		modelID:    config.ModelID,
	}, nil
}
