package rerank

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	lkeapRerankDefaultBaseURL = "https://lkeap.tencentcloudapi.com"
	lkeapRerankAction         = "RunRerank"
	lkeapRerankVersion        = "2024-05-22"
	lkeapRerankService        = "lkeap"
	lkeapRerankDefaultRegion  = "ap-guangzhou"
)

type LKEAPReranker struct {
	modelName string
	modelID   string
	secretID  string
	secretKey string
	baseURL   string
	region    string
	client    *http.Client
}

func NewLKEAPReranker(config *RerankerConfig) (*LKEAPReranker, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("LKEAP reranker: SecretId is required")
	}
	if strings.TrimSpace(config.AppSecret) == "" {
		return nil, fmt.Errorf("LKEAP reranker: SecretKey is required")
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = lkeapRerankDefaultBaseURL
	}
	region := lkeapRerankDefaultRegion
	if config.ExtraConfig != nil && strings.TrimSpace(config.ExtraConfig["region"]) != "" {
		region = strings.TrimSpace(config.ExtraConfig["region"])
	}
	return &LKEAPReranker{
		modelName: strings.TrimSpace(config.ModelName),
		modelID:   config.ModelID,
		secretID:  strings.TrimSpace(config.APIKey),
		secretKey: strings.TrimSpace(config.AppSecret),
		baseURL:   baseURL,
		region:    region,
		client:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

type lkeapRerankRequest struct {
	Query string   `json:"Query"`
	Docs  []string `json:"Docs"`
	Model string   `json:"Model,omitempty"`
}

type lkeapRerankResponse struct {
	Response struct {
		ScoreList []float64 `json:"ScoreList"`
		RequestID string    `json:"RequestId"`
		Error     *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"Response"`
}

func (r *LKEAPReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	originalDocuments := documents
	query, documents, indexMap, err := prepareLKEAPInputs(query, documents)
	if err != nil {
		return nil, err
	}
	reqBody := lkeapRerankRequest{
		Query: query,
		Docs:  documents,
		Model: r.modelName,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("lkeap reranker: marshal: %w", err)
	}

	endpoint, err := url.Parse(r.baseURL)
	if err != nil {
		return nil, fmt.Errorf("lkeap reranker: invalid base URL: %w", err)
	}
	endpoint.Path = "/"
	endpoint.RawQuery = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("lkeap reranker: create request: %w", err)
	}
	req.Host = endpoint.Host
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-TC-Action", lkeapRerankAction)
	req.Header.Set("X-TC-Version", lkeapRerankVersion)
	req.Header.Set("X-TC-Region", r.region)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("Authorization", r.authorization(req, bodyBytes))

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lkeap reranker: do request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lkeap reranker: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lkeap reranker: status %d: %s", resp.StatusCode, string(respBytes))
	}

	var rerankResp lkeapRerankResponse
	if err := json.Unmarshal(respBytes, &rerankResp); err != nil {
		return nil, fmt.Errorf("lkeap reranker: unmarshal: %w", err)
	}
	if rerankResp.Response.Error != nil {
		return nil, fmt.Errorf("lkeap reranker: %s: %s", rerankResp.Response.Error.Code, rerankResp.Response.Error.Message)
	}
	if len(rerankResp.Response.ScoreList) > len(documents) {
		return nil, fmt.Errorf("lkeap reranker: score count %d exceeds document count %d",
			len(rerankResp.Response.ScoreList), len(documents))
	}

	results := make([]RankResult, 0, len(rerankResp.Response.ScoreList))
	for i, score := range rerankResp.Response.ScoreList {
		originalIndex := indexMap[i]
		results = append(results, RankResult{
			Index:          originalIndex,
			Document:       DocumentInfo{Text: originalDocuments[originalIndex]},
			RelevanceScore: score,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	return results, nil
}

func (r *LKEAPReranker) authorization(req *http.Request, payload []byte) string {
	timestamp := req.Header.Get("X-TC-Timestamp")
	date := time.Unix(mustParseInt64(timestamp), 0).UTC().Format("2006-01-02")
	credentialScope := date + "/" + lkeapRerankService + "/tc3_request"
	signedHeaders := "content-type;host;x-tc-action"
	hashedPayload := sha256Hex(payload)
	canonicalHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + req.Host + "\n" +
		"x-tc-action:" + strings.ToLower(req.Header.Get("X-TC-Action")) + "\n"
	canonicalRequest := strings.Join([]string{
		req.Method,
		"/",
		"",
		canonicalHeaders,
		signedHeaders,
		hashedPayload,
	}, "\n")
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		timestamp,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+r.secretKey), date)
	secretService := hmacSHA256(secretDate, lkeapRerankService)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		r.secretID, credentialScope, signedHeaders, signature)
}

func (r *LKEAPReranker) GetModelName() string { return r.modelName }
func (r *LKEAPReranker) GetModelID() string   { return r.modelID }

func prepareLKEAPInputs(query string, documents []string) (string, []string, []int, error) {
	const (
		maxDocs       = 60
		maxTotalRunes = 2000
	)
	queryRunes := []rune(query)
	if len(queryRunes) >= maxTotalRunes {
		return "", nil, nil, fmt.Errorf("lkeap reranker: query length %d exceeds RunRerank limit %d",
			len(queryRunes), maxTotalRunes)
	}
	remaining := maxTotalRunes - len(queryRunes)
	limit := len(documents)
	if limit > maxDocs {
		limit = maxDocs
	}
	limitedDocs := make([]string, 0, limit)
	indexMap := make([]int, 0, limit)
	for i := 0; i < limit && remaining > 0; i++ {
		doc := strings.TrimSpace(documents[i])
		if doc == "" {
			continue
		}
		docRunes := []rune(doc)
		if len(docRunes) > remaining {
			docRunes = docRunes[:remaining]
		}
		limitedDocs = append(limitedDocs, string(docRunes))
		indexMap = append(indexMap, i)
		remaining -= len(docRunes)
	}
	if len(limitedDocs) == 0 {
		return "", nil, nil, fmt.Errorf("lkeap reranker: no documents fit RunRerank input limit")
	}
	return query, limitedDocs, indexMap, nil
}

func totalRunes(values []string) int {
	total := 0
	for _, value := range values {
		total += len([]rune(value))
	}
	return total
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	return mac.Sum(nil)
}

func mustParseInt64(s string) int64 {
	var n int64
	_, _ = fmt.Sscan(s, &n)
	return n
}
