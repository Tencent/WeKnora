// Package tongyi 通义听悟适配层（VP-T004）。
//
// 听悟 OpenAPI 契约（2023-09-30，ROA 签名风格）：
//   - 建任务：PUT /openapi/tingwu/v2/tasks        （Action=CreateTask）
//   - 查任务：GET /openapi/tingwu/v2/tasks/{TaskId}（Action=GetTaskInfo）
//   - 鉴权：  阿里云 ROA V3 签名（ACS3-HMAC-SHA256，AccessKey ID/Secret）
//   - 项目标识：请求体 AppKey（听悟控制台创建的项目 AppKey）
//
// 说明：官方推荐使用 SDK，此处为最小依赖手工实现 ROA V3 签名；
// 字段命名以听悟官方文档为准，联调时若响应结构有出入，仅需调整本文件。
package tongyi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

// 听悟 OpenAPI 常量
const (
	tingwuVersion = "2023-09-30"
	actionCreate  = "CreateTask"
	actionGet     = "GetTaskInfo"
	apiPath       = "/openapi/tingwu/v2/tasks"
)

// Client 通义听悟 HTTP 客户端
type Client struct {
	accessKeyID     string
	accessKeySecret string
	appKey          string
	endpoint        string
	callbackURL     string
	host            string
	http            *http.Client
}

// New 构造听悟 client
func New(cfg config.TongyiConfig) *Client {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	host := strings.TrimPrefix(endpoint, "https://")
	host = strings.TrimPrefix(host, "http://")
	return &Client{
		accessKeyID:     cfg.AccessKeyID,
		accessKeySecret: cfg.AccessKeySecret,
		appKey:          cfg.AppKey,
		endpoint:        endpoint,
		callbackURL:     cfg.CallbackURL,
		host:            host,
		http:            &http.Client{Timeout: 120 * time.Second},
	}
}

// CreateTaskRequest 创建任务参数（听悟离线转写，业务侧平铺字段，序列化时组装为听悟嵌套结构）
type CreateTaskRequest struct {
	AppKey         string
	Type           string // offline / realtime
	SourceLanguage string // cn/en/ja/yue/fspk
	FileURL        string
	SpeakerCount   int // 0 = 自动识别
	CallbackURL    string
}

// createTaskBody 听悟 CreateTask 请求体（嵌套结构）。
// 字段为帕斯卡命名；`type` 不在 body 里，而是查询参数 `?type=offline`。
type createTaskBody struct {
	AppKey     string `json:"AppKey"`
	Input      struct {
		SourceLanguage string `json:"SourceLanguage"`
		FileUrl        string `json:"FileUrl"`
		TaskKey        string `json:"TaskKey,omitempty"`
	} `json:"Input"`
	Parameters struct {
		Transcription struct {
			DiarizationEnabled bool `json:"DiarizationEnabled"`
			Diarization        struct {
				SpeakerCount int `json:"SpeakerCount,omitempty"`
			} `json:"Diarization,omitempty"`
		} `json:"Transcription,omitempty"`
	} `json:"Parameters,omitempty"`
}

// CreateTaskResponse 创建任务返回
type CreateTaskResponse struct {
	TaskID    string `json:"TaskId"`
	RequestID string `json:"RequestId"`
	Status    string `json:"Status"` // SUBMITTED / RUNNING / COMPLETED / FAILED
}

// GetTaskResponse 任务详情
type GetTaskResponse struct {
	TaskID       string `json:"TaskId"`
	Status       string `json:"Status"`
	Progress     int    `json:"Progress"`
	Result       string `json:"Result,omitempty"` // JSON 字符串，含转录结果
	ErrorCode    string `json:"ErrorCode,omitempty"`
	ErrorMessage string `json:"ErrorMessage,omitempty"`
}

// TranscriptResult 听悟转写结果（下载后的内容）
type TranscriptResult struct {
	Transcripts []TranscriptFile `json:"transcripts"`
}

// TranscriptFile 单文件转写结果
type TranscriptFile struct {
	FileURL    string                  `json:"file_url"`
	Paragraphs []SubtitleParagraphLite `json:"paragraphs"`
}

// SubtitleParagraphLite 听悟段落最小字段
type SubtitleParagraphLite struct {
	ParagraphID string                 `json:"paragraph_id"`
	SpeakerID   string                 `json:"speaker_id"`
	StartMs     int                    `json:"start_ms"`
	EndMs       int                    `json:"end_ms"`
	Sentences   []SubtitleSentenceLite `json:"sentences"`
}

// SubtitleSentenceLite 听悟单句最小字段
type SubtitleSentenceLite struct {
	SentenceID string `json:"sentence_id"`
	Text       string `json:"text"`
	StartMs    int    `json:"start_ms"`
	EndMs      int    `json:"end_ms"`
	ChannelID  int    `json:"channel_id"`
}

// CreateTask 发起离线转写任务
func (c *Client) CreateTask(ctx context.Context, req CreateTaskRequest) (*CreateTaskResponse, error) {
	if req.Type == "" {
		req.Type = "offline"
	}
	if req.AppKey == "" {
		req.AppKey = c.appKey
	}
	if req.SourceLanguage == "" {
		req.SourceLanguage = "cn"
	}
	if req.CallbackURL == "" {
		req.CallbackURL = c.callbackURL
	}

	// 组装听悟嵌套请求体
	payload := createTaskBody{
		AppKey: req.AppKey,
	}
	payload.Input.SourceLanguage = req.SourceLanguage
	payload.Input.FileUrl = req.FileURL
	if req.SpeakerCount > 0 {
		payload.Parameters.Transcription.DiarizationEnabled = true
		payload.Parameters.Transcription.Diarization.SpeakerCount = req.SpeakerCount
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := c.newSignedRequest(ctx, http.MethodPut, apiPath, "type="+req.Type, actionCreate, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tingwu create: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tingwu create status %d: %s", resp.StatusCode, string(respBody))
	}

	// 听悟响应包裹：{ Data: { TaskId, TaskStatus }, ... }
	var envelope struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
		Data    struct {
			TaskID     string `json:"TaskId"`
			TaskStatus string `json:"TaskStatus"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode tingwu create: %w", err)
	}
	if envelope.Code != "" && envelope.Code != "0" {
		return nil, fmt.Errorf("tingwu create code %s: %s", envelope.Code, envelope.Message)
	}
	return &CreateTaskResponse{
		TaskID: envelope.Data.TaskID,
		Status: envelope.Data.TaskStatus,
	}, nil
}

// GetTask 查询任务状态与结果
func (c *Client) GetTask(ctx context.Context, taskID string) (*GetTaskResponse, error) {
	path := apiPath + "/" + taskID
	httpReq, err := c.newSignedRequest(ctx, http.MethodGet, path, "", actionGet, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tingwu get: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tingwu get status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Code      string `json:"Code"`
		Message   string `json:"Message"`
		RequestId string `json:"RequestId"`
		Data      struct {
			TaskID     string          `json:"TaskId"`
			TaskStatus string          `json:"TaskStatus"`
			ErrorCode  string          `json:"ErrorCode"`
			ErrorMsg   string          `json:"ErrorMsg"`
			Result     json.RawMessage `json:"Result"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode tingwu get: %w (raw=%s)", err, string(respBody))
	}

	// 听悟业务错误：envelope.Code 非 0 / 非 "0" / 非 "" 时，外层就携带了错误信息。
	// 之前代码忽略 envelope.Code，导致错误信息丢失，日志只看到「听悟失败: 」结尾空。
	if envelope.Code != "" && envelope.Code != "0" {
		return nil, fmt.Errorf("听悟业务错误 Code=%s Message=%s RequestId=%s (task_status=%s)",
			envelope.Code, envelope.Message, envelope.RequestId, envelope.Data.TaskStatus)
	}

	out := &GetTaskResponse{
		TaskID:       envelope.Data.TaskID,
		Status:       envelope.Data.TaskStatus,
		Result:       string(envelope.Data.Result),
		ErrorCode:    envelope.Data.ErrorCode,
		ErrorMessage: envelope.Data.ErrorMsg,
	}
	// 若 status=FAILED 但 Data 内没填 ErrorCode/ErrorMsg，则用外层 Message 兜底
	if out.Status == "FAILED" && out.ErrorMessage == "" {
		out.ErrorMessage = envelope.Message
	}
	return out, nil
}

// DownloadResult 下载并解析转写结果。
// 听悟 GetTask 的 Result 字段形如 {"Transcription":"<转写文件下载链接>"}，
// 需先 GET 该链接拿到真正的转写文件，再把其中的 Words（词）按 SentenceId 聚合成句子。
func (c *Client) DownloadResult(ctx context.Context, raw string) (*TranscriptResult, error) {
	// 1. 解析 Result 外层，拿到转写文件下载链接
	var res struct {
		Transcription string `json:"Transcription"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, fmt.Errorf("parse tingwu result: %w", err)
	}
	if res.Transcription == "" {
		return nil, fmt.Errorf("听悟结果缺少 Transcription 下载链接")
	}

	// 2. 下载转写文件
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, res.Transcription, nil)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("download transcript: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download transcript status %d", resp.StatusCode)
	}

	// 3. 解析转写文件：{"Transcription":{"Paragraphs":[{"ParagraphId","SpeakerId","Words":[...]}]}}
	var file struct {
		Transcription struct {
			Paragraphs []struct {
				ParagraphID string `json:"ParagraphId"`
				SpeakerID   string `json:"SpeakerId"`
				Words       []struct {
					SentenceID int    `json:"SentenceId"`
					Start      int    `json:"Start"`
					End        int    `json:"End"`
					Text       string `json:"Text"`
				} `json:"Words"`
			} `json:"Paragraphs"`
		} `json:"Transcription"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("decode transcript file: %w", err)
	}

	// 4. Words 按 SentenceId 聚合成 Sentences
	tf := TranscriptFile{FileURL: res.Transcription}
	for _, p := range file.Transcription.Paragraphs {
		para := SubtitleParagraphLite{ParagraphID: p.ParagraphID, SpeakerID: p.SpeakerID}
		var cur *SubtitleSentenceLite
		for _, w := range p.Words {
			sid := fmt.Sprintf("%d", w.SentenceID)
			if cur == nil || cur.SentenceID != sid {
				para.Sentences = append(para.Sentences, SubtitleSentenceLite{
					SentenceID: sid,
					StartMs:    w.Start,
					EndMs:      w.End,
					Text:       w.Text,
				})
				cur = &para.Sentences[len(para.Sentences)-1]
			} else {
				cur.Text += w.Text
				cur.EndMs = w.End
			}
		}
		if len(para.Sentences) > 0 {
			para.StartMs = para.Sentences[0].StartMs
			para.EndMs = para.Sentences[len(para.Sentences)-1].EndMs
		}
		tf.Paragraphs = append(tf.Paragraphs, para)
	}

	return &TranscriptResult{Transcripts: []TranscriptFile{tf}}, nil
}

// newSignedRequest 构造带 ROA V3 签名的请求。
// body 为空表示 GET（无请求体，content-sha256 用空串哈希）。
func (c *Client) newSignedRequest(ctx context.Context, method, path, query, action string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	url := c.endpoint + path
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// 计算 ROA V3 签名所需的 headers
	bodyHash := sha256Hex(body)
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	nonce := newNonce()

	signed := map[string]string{
		"content-type":          "application/json",
		"host":                  c.host,
		"x-acs-action":          action,
		"x-acs-content-sha256":  bodyHash,
		"x-acs-date":            date,
		"x-acs-signature-nonce": nonce,
		"x-acs-version":         tingwuVersion,
	}
	keys := make([]string, 0, len(signed))
	for k := range signed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(signed[k])
		canonicalHeaders.WriteString("\n")
	}
	signedHeaderNames := strings.Join(keys, ";")
	canonicalRequest := strings.Join([]string{
		method,
		path,
		query, // canonical query string
		canonicalHeaders.String(),
		signedHeaderNames,
		bodyHash,
	}, "\n")

	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))
	signature := hmacSha256Hex(c.accessKeySecret, stringToSign)

	authorization := fmt.Sprintf(
		"ACS3-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%s",
		c.accessKeyID, signedHeaderNames, signature,
	)

	req.Header.Set("Authorization", authorization)
	req.Header.Set("x-acs-action", action)
	req.Header.Set("x-acs-content-sha256", bodyHash)
	req.Header.Set("x-acs-date", date)
	req.Header.Set("x-acs-signature-nonce", nonce)
	req.Header.Set("x-acs-version", tingwuVersion)

	return req, nil
}

// sha256Hex 计算 sha256 十六进制摘要
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hmacSha256Hex 计算 HMAC-SHA256 十六进制
func hmacSha256Hex(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// newNonce 生成签名 nonce（16 字节随机，hex 编码）
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
