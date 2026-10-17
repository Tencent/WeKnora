package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/video"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type debugVideoService struct {
	interfaces.ModelService
	row   *types.Model
	model *debugVideoModel
}

func (s *debugVideoService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.row, nil
}

func (s *debugVideoService) GetVideoModel(context.Context, string) (video.Video, error) {
	return s.model, nil
}

type debugVideoModel struct {
	answer   string
	err      error
	bytes    []byte
	fileName string
	prompt   string
}

func (m *debugVideoModel) Predict(
	_ context.Context, data []byte, fileName, prompt string,
) (string, error) {
	m.bytes = data
	m.fileName = fileName
	m.prompt = prompt
	return m.answer, m.err
}

func (m *debugVideoModel) PredictURL(context.Context, string, string) (string, error) {
	return "", m.err
}

func (m *debugVideoModel) GetModelName() string { return "video-debug" }
func (m *debugVideoModel) GetModelID() string   { return "video-1" }

func runVideoDebug(
	t *testing.T, svc *debugVideoService, input string, data []byte,
) (map[string]any, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("input", input))
	require.NoError(t, w.WriteField("documents", ""))
	if data != nil {
		f, err := w.CreateFormFile("file", "lesson.mp4")
		require.NoError(t, err)
		_, err = f.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/models/test/debug", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: "test"}}
	(&ModelHandler{service: svc}).DebugModel(c)
	if len(c.Errors) > 0 {
		return nil, c.Errors.Last().Error()
	}
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Data, ""
}

// The model test's video tab sends a video file to the same debug endpoint. A
// video must reach the video model untouched: the image preparation that the
// other types go through would decode the file as pixels and reject it.
func TestModelDebugVideoPassesTheFileToTheVideoModel(t *testing.T) {
	model := &debugVideoModel{answer: "这段视频演示了如何配置模型。"}
	svc := &debugVideoService{row: &types.Model{Type: types.ModelTypeVideo}, model: model}
	mp4 := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}

	result, errText := runVideoDebug(t, svc, "这段视频讲了什么？", mp4)
	require.Empty(t, errText)
	require.Equal(t, true, result["ok"])
	require.Equal(t, model.answer, result["raw_response"])
	require.Equal(t, mp4, model.bytes, "the bytes must reach the model unchanged")
	require.Equal(t, "lesson.mp4", model.fileName)
	require.Equal(t, "这段视频讲了什么？", model.prompt)
	require.Equal(t,
		float64(len([]rune(model.answer))),
		result["observations"].(map[string]any)["answer_characters"])
}

// Without a file there is nothing to send: the request is refused before any
// model is resolved.
func TestModelDebugVideoRequiresAFile(t *testing.T) {
	model := &debugVideoModel{answer: "unused"}
	svc := &debugVideoService{row: &types.Model{Type: types.ModelTypeVideo}, model: model}

	_, errText := runVideoDebug(t, svc, "describe", nil)
	require.Contains(t, errText, "video file is required")
	require.Nil(t, model.bytes)
}

// An upstream failure is reported as a failed test, not as a 500: the drawer
// renders the error next to the request that caused it.
func TestModelDebugVideoReportsAModelFailure(t *testing.T) {
	model := &debugVideoModel{err: context.DeadlineExceeded}
	svc := &debugVideoService{row: &types.Model{Type: types.ModelTypeVideo}, model: model}

	result, errText := runVideoDebug(t, svc, "", []byte("mp4"))
	require.Empty(t, errText)
	require.Equal(t, false, result["ok"])
	require.Contains(t, result["error"], "context deadline exceeded")
}
