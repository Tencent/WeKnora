package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	generationPromptVersion   = "qa-from-passage-v1"
	maxGenerationResponseSize = 4 << 20
)

type GenerationConnection struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	BaseURL               string    `json:"base_url"`
	APIKey                string    `json:"api_key,omitempty"`
	Model                 string    `json:"model"`
	TestPrompt            string    `json:"test_prompt,omitempty"`
	TimeoutSeconds        int       `json:"timeout_seconds"`
	QuestionsPerPassage   int       `json:"questions_per_passage"`
	MaxOutputTokens       int       `json:"max_output_tokens"`
	MaxTotalTokens        int       `json:"max_total_tokens"`
	InputPricePerMillion  float64   `json:"input_price_per_million,omitempty"`
	OutputPricePerMillion float64   `json:"output_price_per_million,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type GenerationIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type GenerationCandidate struct {
	ID                 string            `json:"id"`
	PassageIDs         []int64           `json:"passage_ids"`
	Question           string            `json:"question"`
	Answer             string            `json:"answer"`
	AnswerKeyPoints    []string          `json:"answer_key_points"`
	Category           string            `json:"category,omitempty"`
	Difficulty         string            `json:"difficulty,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	Answerable         bool              `json:"answerable"`
	EvidenceQuotes     []string          `json:"evidence_quotes,omitempty"`
	Issues             []GenerationIssue `json:"issues"`
	ReviewStatus       string            `json:"review_status"`
	ApprovedQuestionID int64             `json:"approved_question_id,omitempty"`
}

type GenerationUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type GenerationJob struct {
	ID                   string                `json:"id"`
	ProjectID            string                `json:"project_id"`
	DatasetCreatedAt     time.Time             `json:"dataset_created_at"`
	DatasetVersion       string                `json:"dataset_version"`
	ConnectionID         string                `json:"connection_id"`
	Model                string                `json:"model"`
	PromptVersion        string                `json:"prompt_version"`
	PassageIDs           []int64               `json:"passage_ids"`
	PassageSHA256        map[int64]string      `json:"passage_sha256"`
	QuestionsPerPassage  int                   `json:"questions_per_passage"`
	RequestedCategory    string                `json:"requested_category,omitempty"`
	RequestedDifficulty  string                `json:"requested_difficulty,omitempty"`
	AdditionalGuidance   string                `json:"additional_guidance,omitempty"`
	Status               string                `json:"status"`
	CancelRequested      bool                  `json:"cancel_requested,omitempty"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
	StartedAt            *time.Time            `json:"started_at,omitempty"`
	FinishedAt           *time.Time            `json:"finished_at,omitempty"`
	TotalPassages        int                   `json:"total_passages"`
	CompletedPassages    int                   `json:"completed_passages"`
	FailedPassages       int                   `json:"failed_passages"`
	CacheHits            int                   `json:"cache_hits"`
	EstimatedInputTokens int                   `json:"estimated_input_tokens"`
	TokenBudget          int                   `json:"token_budget"`
	Usage                GenerationUsage       `json:"usage"`
	EstimatedCost        float64               `json:"estimated_cost"`
	ActualEstimatedCost  float64               `json:"actual_estimated_cost"`
	Candidates           []GenerationCandidate `json:"candidates"`
	Errors               []string              `json:"errors,omitempty"`
}

type generationStartRequest struct {
	ConnectionID        string  `json:"connection_id"`
	PassageIDs          []int64 `json:"passage_ids"`
	QuestionsPerPassage int     `json:"questions_per_passage"`
	Category            string  `json:"category,omitempty"`
	Difficulty          string  `json:"difficulty,omitempty"`
	AdditionalGuidance  string  `json:"additional_guidance,omitempty"`
	Regenerate          bool    `json:"regenerate,omitempty"`
}

type generationReviewItem struct {
	CandidateID     string   `json:"candidate_id"`
	Action          string   `json:"action"`
	Question        string   `json:"question,omitempty"`
	Answer          string   `json:"answer,omitempty"`
	AnswerKeyPoints []string `json:"answer_key_points,omitempty"`
	Category        string   `json:"category,omitempty"`
	Difficulty      string   `json:"difficulty,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	EvidenceQuotes  []string `json:"evidence_quotes,omitempty"`
}

type generationReviewRequest struct {
	Items []generationReviewItem `json:"items"`
}

type generatedCandidatePayload struct {
	Question        string   `json:"question"`
	Answer          string   `json:"answer"`
	AnswerKeyPoints []string `json:"answer_key_points"`
	Category        string   `json:"category"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
	Answerable      *bool    `json:"answerable"`
	EvidenceQuotes  []string `json:"evidence_quotes"`
}

type generationModelPayload struct {
	Candidates []generatedCandidatePayload `json:"candidates"`
}

type generationCacheEntry struct {
	Candidates []generatedCandidatePayload `json:"candidates"`
	CreatedAt  time.Time                   `json:"created_at"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

var generationHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("不允许生成模型 API 重定向")
	},
}

func normalizeGenerationConnection(profile *GenerationConnection) error {
	if profile == nil {
		return errors.New("生成模型配置不能为空")
	}
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.BaseURL = strings.TrimSpace(strings.TrimRight(profile.BaseURL, "/"))
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.TestPrompt = strings.TrimSpace(profile.TestPrompt)
	if err := validateDatasetID(profile.ID); err != nil {
		return errors.New(strings.NewReplacer("数据集 ID", "生成配置 ID").Replace(err.Error()))
	}
	if profile.Name == "" || profile.Model == "" || profile.APIKey == "" {
		return errors.New("配置名称、模型名称和 API Key 不能为空")
	}
	if profile.TestPrompt == "" {
		profile.TestPrompt = "回复1"
	}
	if utf8.RuneCountInString(profile.TestPrompt) > 200 {
		return errors.New("测试提示词不能超过 200 字符")
	}
	parsed, err := url.Parse(profile.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("生成模型地址必须是有效的 http 或 https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("生成模型地址不能包含用户信息、查询参数或片段")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Host) {
		return errors.New("非本机生成模型必须使用 HTTPS")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/v1"
	} else if !strings.HasSuffix(path, "/v1") {
		return errors.New("生成模型地址路径必须为空或以 /v1 结尾")
	}
	parsed.Path = path
	profile.BaseURL = strings.TrimRight(parsed.String(), "/")
	if profile.TimeoutSeconds == 0 {
		profile.TimeoutSeconds = 60
	}
	if profile.TimeoutSeconds < 5 || profile.TimeoutSeconds > 300 {
		return errors.New("超时时间必须在 5～300 秒之间")
	}
	if profile.QuestionsPerPassage == 0 {
		profile.QuestionsPerPassage = 1
	}
	if profile.QuestionsPerPassage < 1 || profile.QuestionsPerPassage > 5 {
		return errors.New("每段生成题数必须在 1～5 之间")
	}
	if profile.MaxOutputTokens == 0 {
		profile.MaxOutputTokens = 1200
	}
	if profile.MaxOutputTokens < 256 {
		return errors.New("单次最大输出 token 不能小于 256")
	}
	if profile.MaxTotalTokens == 0 {
		profile.MaxTotalTokens = 20000
	}
	if profile.MaxTotalTokens < profile.MaxOutputTokens {
		return errors.New("任务 token 上限必须不小于单次输出上限")
	}
	if profile.InputPricePerMillion < 0 || profile.OutputPricePerMillion < 0 || math.IsNaN(profile.InputPricePerMillion) || math.IsNaN(profile.OutputPricePerMillion) {
		return errors.New("token 单价不能为负数或无效数值")
	}
	return nil
}

func (s *projectStore) saveGenerationConnection(id string, profile *GenerationConnection) (*GenerationConnection, error) {
	if profile == nil || (id != "" && strings.TrimSpace(id) != strings.TrimSpace(profile.ID)) {
		return nil, errors.New("生成配置 ID 不一致")
	}
	profile.ID = strings.TrimSpace(profile.ID)
	if err := validateDatasetID(profile.ID); err != nil {
		return nil, err
	}
	path := filepath.Join(s.generationConnections, profile.ID+".json")
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		var existing GenerationConnection
		if json.Unmarshal(data, &existing) == nil {
			profile.CreatedAt = existing.CreatedAt
			if strings.TrimSpace(profile.APIKey) == "" {
				profile.APIKey = existing.APIKey
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := normalizeGenerationConnection(profile); err != nil {
		return nil, err
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeBytesAtomic(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	copy := *profile
	return &copy, nil
}

func (s *projectStore) listGenerationConnections() ([]GenerationConnection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.generationConnections)
	if err != nil {
		return nil, err
	}
	items := make([]GenerationConnection, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.generationConnections, entry.Name()))
		if err != nil {
			return nil, err
		}
		var item GenerationConnection
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (s *projectStore) getGenerationConnection(id string) (*GenerationConnection, error) {
	if err := validateDatasetID(strings.TrimSpace(id)); err != nil {
		return nil, errors.New("生成模型配置不存在")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.generationConnections, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("生成模型配置不存在")
	}
	if err != nil {
		return nil, err
	}
	var item GenerationConnection
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *projectStore) deleteGenerationConnection(id string) error {
	if err := validateDatasetID(strings.TrimSpace(id)); err != nil {
		return errors.New("生成模型配置不存在")
	}
	if err := s.ensureConfigurationNotReferenced("generation", id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.generationConnections, id+".json")); errors.Is(err, fs.ErrNotExist) {
		return errors.New("生成模型配置不存在")
	} else {
		return err
	}
}

func (s *projectStore) testGenerationConnection(id string) error {
	profile, err := s.getGenerationConnection(id)
	if err != nil {
		return err
	}
	prompt := strings.TrimSpace(profile.TestPrompt)
	if prompt == "" {
		prompt = "回复1"
	}
	_, _, err = requestGenerationModel(profile, prompt, 1, false)
	return err
}

func estimateTextTokens(value string) int {
	count := utf8.RuneCountInString(value)
	if count == 0 {
		return 0
	}
	return (count + 1) / 2
}

func generationPrompt(passage Passage, count int, category, difficulty, guidance string) string {
	return fmt.Sprintf(`你是知识库评测数据集设计专家。请只根据给定语料生成 %d 道自然、明确、可回答的中文测试问题。

要求：
1. 标准答案必须完全受语料支持，不得补充语料之外的事实。
2. 每道题提供 1～6 个答案核心要点。
3. evidence_quotes 必须逐字引用语料中的短句，用来证明答案依据。
4. 问题不能出现“根据上述材料”“本文”等上下文依赖表达。
5. difficulty 只能是 easy、medium 或 hard。
6. 只返回 JSON 对象，不要 Markdown 代码块或解释。

期望结构：{"candidates":[{"question":"...","answer":"...","answer_key_points":["..."],"category":"...","difficulty":"easy","tags":["..."],"answerable":true,"evidence_quotes":["..."]}]}

期望分类：%s
期望难度：%s
补充要求：%s

语料 ID：%d
语料来源：%s
语料正文：
%s`, count, defaultGenerationValue(category), defaultGenerationValue(difficulty), defaultGenerationValue(guidance), passage.ID, defaultGenerationValue(passage.Source), passage.Text)
}

func defaultGenerationValue(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "未指定，由模型合理判断"
}

func generationConnectionCost(profile *GenerationConnection, input, output int) float64 {
	return float64(input)/1000000*profile.InputPricePerMillion + float64(output)/1000000*profile.OutputPricePerMillion
}

func (s *projectStore) startGenerationJob(projectID string, req generationStartRequest) (*GenerationJob, error) {
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	profile, err := s.getGenerationConnection(strings.TrimSpace(req.ConnectionID))
	if err != nil {
		return nil, err
	}
	if len(req.PassageIDs) == 0 || len(req.PassageIDs) > 100 {
		return nil, errors.New("请选择 1～100 条语料")
	}
	count := req.QuestionsPerPassage
	if count == 0 {
		count = profile.QuestionsPerPassage
	}
	if count < 1 || count > 5 {
		return nil, errors.New("每段生成题数必须在 1～5 之间")
	}
	req.Category = strings.TrimSpace(req.Category)
	req.Difficulty = strings.TrimSpace(req.Difficulty)
	if req.Difficulty != "" && req.Difficulty != "easy" && req.Difficulty != "medium" && req.Difficulty != "hard" {
		return nil, errors.New("难度只能是 easy、medium 或 hard")
	}
	if utf8.RuneCountInString(req.AdditionalGuidance) > 1000 {
		return nil, errors.New("补充要求不能超过 1000 字符")
	}
	passageByID := make(map[int64]Passage, len(project.Passages))
	for _, passage := range project.Passages {
		passageByID[passage.ID] = passage
	}
	ids := uniqueSortedInt64(req.PassageIDs)
	hashes := make(map[int64]string, len(ids))
	estimatedInput := 0
	for _, id := range ids {
		passage, ok := passageByID[id]
		if !ok {
			return nil, fmt.Errorf("语料 ID %d 不存在", id)
		}
		if strings.TrimSpace(passage.Text) == "" {
			return nil, fmt.Errorf("语料 ID %d 内容为空", id)
		}
		hash := sha256.Sum256([]byte(passage.Text))
		hashes[id] = hex.EncodeToString(hash[:])
		estimatedInput += estimateTextTokens(generationPrompt(passage, count, req.Category, req.Difficulty, req.AdditionalGuidance))
	}
	estimatedOutput := len(ids) * profile.MaxOutputTokens
	if estimatedInput+estimatedOutput > profile.MaxTotalTokens {
		return nil, fmt.Errorf("预计 token 上限为 %d，超过配置的任务上限 %d；请减少语料或提高预算", estimatedInput+estimatedOutput, profile.MaxTotalTokens)
	}
	now := s.now().UTC()
	job := &GenerationJob{
		ID: historyID(now), ProjectID: project.ID, DatasetCreatedAt: project.CreatedAt, DatasetVersion: project.Version,
		ConnectionID: profile.ID, Model: profile.Model, PromptVersion: generationPromptVersion,
		PassageIDs: ids, PassageSHA256: hashes, QuestionsPerPassage: count,
		RequestedCategory: req.Category, RequestedDifficulty: req.Difficulty, AdditionalGuidance: strings.TrimSpace(req.AdditionalGuidance),
		Status: "pending", CreatedAt: now, UpdatedAt: now, TotalPassages: len(ids),
		EstimatedInputTokens: estimatedInput, TokenBudget: profile.MaxTotalTokens,
		EstimatedCost: generationConnectionCost(profile, estimatedInput, estimatedOutput), Candidates: []GenerationCandidate{}, Errors: []string{},
	}
	if err := s.writeGenerationJob(job); err != nil {
		return nil, err
	}
	go s.runGenerationJob(project.ID, job.ID, profile.APIKey, req.Regenerate)
	return job, nil
}

func uniqueSortedInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s *projectStore) generationJobPath(projectID, jobID string) (string, error) {
	if err := validateDatasetID(projectID); err != nil {
		return "", err
	}
	if err := validateHistoryID(jobID); err != nil {
		return "", err
	}
	return filepath.Join(s.generationJobs, projectID, jobID+".json"), nil
}

func (s *projectStore) writeGenerationJob(job *GenerationJob) error {
	path, err := s.generationJobPath(job.ProjectID, job.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeBytesAtomic(path, append(data, '\n'), 0o640)
}

func (s *projectStore) getGenerationJob(projectID, jobID string) (*GenerationJob, error) {
	path, err := s.generationJobPath(projectID, jobID)
	if err != nil {
		return nil, err
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("生成任务不存在")
	}
	if err != nil {
		return nil, err
	}
	var job GenerationJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	if !job.DatasetCreatedAt.Equal(project.CreatedAt) {
		return nil, errors.New("生成任务属于同名数据集的其他生命周期")
	}
	return &job, nil
}

func (s *projectStore) listGenerationJobs(projectID string) ([]GenerationJob, error) {
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.generationJobs, projectID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []GenerationJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]GenerationJob, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var item GenerationJob
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		if item.DatasetCreatedAt.Equal(project.CreatedAt) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *projectStore) cancelGenerationJob(projectID, jobID string) (*GenerationJob, error) {
	job, err := s.getGenerationJob(projectID, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != "pending" && job.Status != "running" {
		return nil, errors.New("只有等待或运行中的任务可以取消")
	}
	job.CancelRequested = true
	job.UpdatedAt = s.now().UTC()
	if err := s.writeGenerationJob(job); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *projectStore) runGenerationJob(projectID, jobID, apiKey string, regenerate bool) {
	job, err := s.getGenerationJob(projectID, jobID)
	if err != nil {
		return
	}
	profile, err := s.getGenerationConnection(job.ConnectionID)
	if err != nil {
		s.failGenerationJob(job, err)
		return
	}
	profile.APIKey = apiKey
	project, err := s.get(projectID)
	if err != nil {
		s.failGenerationJob(job, err)
		return
	}
	passageByID := make(map[int64]Passage, len(project.Passages))
	for _, passage := range project.Passages {
		passageByID[passage.ID] = passage
	}
	now := s.now().UTC()
	job.Status = "running"
	job.StartedAt = &now
	job.UpdatedAt = now
	_ = s.writeGenerationJob(job)
	existingQuestions := make([]string, 0, len(project.Questions))
	for _, question := range project.Questions {
		existingQuestions = append(existingQuestions, question.Text)
	}
	for _, passageID := range job.PassageIDs {
		fresh, loadErr := s.getGenerationJob(projectID, jobID)
		if loadErr != nil {
			return
		}
		job = fresh
		if job.CancelRequested {
			job.Status = "canceled"
			finished := s.now().UTC()
			job.FinishedAt = &finished
			job.UpdatedAt = finished
			_ = s.writeGenerationJob(job)
			return
		}
		if job.Usage.TotalTokens >= job.TokenBudget {
			job.Status = "failed"
			job.Errors = append(job.Errors, "已达到任务 token 上限，剩余语料未发送")
			break
		}
		passage := passageByID[passageID]
		prompt := generationPrompt(passage, job.QuestionsPerPassage, job.RequestedCategory, job.RequestedDifficulty, job.AdditionalGuidance)
		cacheKey := generationCacheKey(profile, passage, job)
		var payload generationModelPayload
		var usage GenerationUsage
		if !regenerate {
			if cached, ok := s.readGenerationCache(cacheKey); ok {
				payload.Candidates = cached.Candidates
				job.CacheHits++
			}
		}
		if len(payload.Candidates) == 0 {
			var generationErr error
			for attempt := 0; attempt < 2; attempt++ {
				content, modelUsage, callErr := callGenerationModel(profile, prompt, profile.MaxOutputTokens)
				if callErr != nil {
					generationErr = callErr
					continue
				}
				usage = modelUsage
				generationErr = parseGenerationPayload(content, &payload, job.QuestionsPerPassage)
				if generationErr == nil {
					break
				}
			}
			if generationErr != nil {
				job.FailedPassages++
				job.Errors = append(job.Errors, fmt.Sprintf("语料 %d（已重试 1 次）：%s", passageID, generationErr.Error()))
				job.UpdatedAt = s.now().UTC()
				_ = s.writeGenerationJob(job)
				continue
			}
			_ = s.writeGenerationCache(cacheKey, generationCacheEntry{Candidates: payload.Candidates, CreatedAt: s.now().UTC()})
		}
		if fresh, loadErr := s.getGenerationJob(projectID, jobID); loadErr == nil {
			job.CancelRequested = fresh.CancelRequested
		}
		job.Usage.PromptTokens += usage.PromptTokens
		job.Usage.CompletionTokens += usage.CompletionTokens
		job.Usage.TotalTokens += usage.TotalTokens
		for _, raw := range payload.Candidates {
			candidate := buildGenerationCandidate(raw, passage, existingQuestions, job.Candidates)
			candidate.ID = fmt.Sprintf("c%04d", len(job.Candidates)+1)
			job.Candidates = append(job.Candidates, candidate)
		}
		job.CompletedPassages++
		job.ActualEstimatedCost = generationConnectionCost(profile, job.Usage.PromptTokens, job.Usage.CompletionTokens)
		job.UpdatedAt = s.now().UTC()
		_ = s.writeGenerationJob(job)
	}
	if fresh, loadErr := s.getGenerationJob(projectID, jobID); loadErr == nil && fresh.CancelRequested {
		job.CancelRequested = true
		job.Status = "canceled"
	}
	finished := s.now().UTC()
	if job.Status != "failed" && job.Status != "canceled" {
		if job.CompletedPassages == 0 && job.FailedPassages > 0 {
			job.Status = "failed"
		} else {
			job.Status = "succeeded"
		}
	}
	job.FinishedAt = &finished
	job.UpdatedAt = finished
	_ = s.writeGenerationJob(job)
}

func (s *projectStore) failGenerationJob(job *GenerationJob, err error) {
	job.Status = "failed"
	job.Errors = append(job.Errors, err.Error())
	now := s.now().UTC()
	job.UpdatedAt = now
	job.FinishedAt = &now
	_ = s.writeGenerationJob(job)
}

func generationCacheKey(profile *GenerationConnection, passage Passage, job *GenerationJob) string {
	data, _ := json.Marshal([]any{generationPromptVersion, profile.BaseURL, profile.Model, passage.Text, job.QuestionsPerPassage, job.RequestedCategory, job.RequestedDifficulty, job.AdditionalGuidance})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *projectStore) readGenerationCache(key string) (*generationCacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.generationCache, key+".json"))
	if err != nil {
		return nil, false
	}
	var item generationCacheEntry
	if json.Unmarshal(data, &item) != nil || len(item.Candidates) == 0 {
		return nil, false
	}
	return &item, true
}

func (s *projectStore) writeGenerationCache(key string, item generationCacheEntry) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeBytesAtomic(filepath.Join(s.generationCache, key+".json"), data, 0o640)
}

func callGenerationModel(profile *GenerationConnection, prompt string, maxTokens int) (string, GenerationUsage, error) {
	return requestGenerationModel(profile, prompt, maxTokens, true)
}

func requestGenerationModel(profile *GenerationConnection, prompt string, maxTokens int, requireContent bool) (string, GenerationUsage, error) {
	body, err := json.Marshal(map[string]any{
		"model":       profile.Model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.2,
		"max_tokens":  maxTokens,
	})
	if err != nil {
		return "", GenerationUsage{}, err
	}
	request, err := http.NewRequest(http.MethodPost, profile.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", GenerationUsage{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+profile.APIKey)
	client := *generationHTTPClient
	client.Timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	response, err := client.Do(request)
	if err != nil {
		return "", GenerationUsage{}, errors.New("无法连接生成模型；已隐藏目标地址和底层网络错误")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGenerationResponseSize+1))
	if err != nil || len(data) > maxGenerationResponseSize {
		return "", GenerationUsage{}, errors.New("生成模型响应读取失败或超过 4 MiB")
	}
	var envelope openAIChatResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", GenerationUsage{}, fmt.Errorf("生成模型返回无效 JSON（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = fmt.Sprintf("请求失败（HTTP %d）", response.StatusCode)
		}
		message = remoteSensitiveValuePattern.ReplaceAllString(strings.Join(strings.Fields(message), " "), "[已脱敏]")
		if len([]rune(message)) > 300 {
			message = string([]rune(message)[:300]) + "…"
		}
		return "", GenerationUsage{}, errors.New(message)
	}
	content := ""
	if len(envelope.Choices) > 0 {
		content = envelope.Choices[0].Message.Content
	}
	if requireContent && strings.TrimSpace(content) == "" {
		return "", GenerationUsage{}, errors.New("生成模型响应缺少文本内容")
	}
	usage := GenerationUsage{PromptTokens: envelope.Usage.PromptTokens, CompletionTokens: envelope.Usage.CompletionTokens, TotalTokens: envelope.Usage.TotalTokens}
	if usage.TotalTokens == 0 {
		usage.PromptTokens = estimateTextTokens(prompt)
		usage.CompletionTokens = estimateTextTokens(content)
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return content, usage, nil
}

func parseGenerationPayload(content string, target *generationModelPayload, expectedCount int) error {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("模型未返回符合契约的候选 JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("模型响应中只能包含一个候选 JSON 对象")
	}
	if len(target.Candidates) != expectedCount {
		return fmt.Errorf("模型返回 %d 个候选，与请求的 %d 个不一致", len(target.Candidates), expectedCount)
	}
	for _, candidate := range target.Candidates {
		if candidate.Answerable == nil {
			return errors.New("模型候选缺少 answerable 字段")
		}
	}
	return nil
}

func buildGenerationCandidate(raw generatedCandidatePayload, passage Passage, existing []string, candidates []GenerationCandidate) GenerationCandidate {
	answerable := true
	if raw.Answerable != nil {
		answerable = *raw.Answerable
	}
	candidate := GenerationCandidate{
		PassageIDs: []int64{passage.ID}, Question: strings.TrimSpace(raw.Question), Answer: strings.TrimSpace(raw.Answer),
		AnswerKeyPoints: cleanStrings(raw.AnswerKeyPoints), Category: strings.TrimSpace(raw.Category), Difficulty: strings.TrimSpace(raw.Difficulty),
		Tags: cleanStrings(raw.Tags), Answerable: answerable, EvidenceQuotes: cleanStrings(raw.EvidenceQuotes), Issues: []GenerationIssue{}, ReviewStatus: "pending",
	}
	candidate.Issues = validateGenerationCandidate(candidate, passage, existing, candidates)
	return candidate
}

func cleanStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func validateGenerationCandidate(candidate GenerationCandidate, passage Passage, existing []string, candidates []GenerationCandidate) []GenerationIssue {
	issues := []GenerationIssue{}
	add := func(severity, code, message string) {
		issues = append(issues, GenerationIssue{Severity: severity, Code: code, Message: message})
	}
	questionLength := utf8.RuneCountInString(candidate.Question)
	if questionLength < 4 || questionLength > 500 {
		add("error", "question_length", "问题长度必须在 4～500 字符之间")
	}
	answerLength := utf8.RuneCountInString(candidate.Answer)
	if answerLength < 2 || answerLength > 4000 {
		add("error", "answer_length", "答案长度必须在 2～4000 字符之间")
	}
	if len(candidate.AnswerKeyPoints) == 0 || len(candidate.AnswerKeyPoints) > 10 {
		add("error", "answer_key_points", "答案核心要点必须为 1～10 条")
	}
	if candidate.Difficulty != "easy" && candidate.Difficulty != "medium" && candidate.Difficulty != "hard" {
		add("error", "difficulty", "难度只能是 easy、medium 或 hard")
	}
	if strings.Contains(candidate.Question, "上述") || strings.Contains(candidate.Question, "本文") || strings.Contains(candidate.Question, "材料") {
		add("warning", "context_dependent", "问题可能依赖“上述材料”等上下文")
	}
	if len(candidate.EvidenceQuotes) == 0 {
		add("error", "evidence_required", "至少需要一条语料证据引用")
	}
	normalizedPassage := normalizeEvidence(passage.Text)
	for _, quote := range candidate.EvidenceQuotes {
		if !strings.Contains(normalizedPassage, normalizeEvidence(quote)) {
			add("error", "evidence_not_found", fmt.Sprintf("证据未在语料中找到：%s", quote))
		}
	}
	for _, value := range existing {
		if normalizeEvidence(value) == normalizeEvidence(candidate.Question) {
			add("error", "question_duplicate", "问题与当前数据集已有问题重复")
		} else if textJaccard(value, candidate.Question) >= 0.85 {
			add("warning", "question_similar", "问题与当前数据集已有问题高度相似")
		}
	}
	for _, value := range candidates {
		if normalizeEvidence(value.Question) == normalizeEvidence(candidate.Question) {
			add("error", "candidate_duplicate", "问题与本次任务的其他候选重复")
		} else if textJaccard(value.Question, candidate.Question) >= 0.85 {
			add("warning", "candidate_similar", "问题与本次任务的其他候选高度相似")
		}
	}
	return issues
}

func normalizeEvidence(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func textJaccard(left, right string) float64 {
	grams := func(value string) map[string]struct{} {
		runes := []rune(normalizeEvidence(value))
		result := map[string]struct{}{}
		if len(runes) < 2 {
			result[string(runes)] = struct{}{}
			return result
		}
		for i := 0; i < len(runes)-1; i++ {
			result[string(runes[i:i+2])] = struct{}{}
		}
		return result
	}
	a, b := grams(left), grams(right)
	intersection := 0
	for value := range a {
		if _, ok := b[value]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func (s *projectStore) reviewGenerationCandidates(projectID, jobID string, req generationReviewRequest) (*GenerationJob, *DatasetProject, error) {
	if len(req.Items) == 0 {
		return nil, nil, errors.New("请选择至少一条候选")
	}
	job, err := s.getGenerationJob(projectID, jobID)
	if err != nil {
		return nil, nil, err
	}
	if job.Status != "succeeded" && job.Status != "canceled" {
		return nil, nil, errors.New("只有已完成或已取消的任务可以审核候选")
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, nil, err
	}
	passageByID := make(map[int64]Passage, len(project.Passages))
	for _, passage := range project.Passages {
		passageByID[passage.ID] = passage
	}
	candidateIndex := make(map[string]int, len(job.Candidates))
	for index, candidate := range job.Candidates {
		candidateIndex[candidate.ID] = index
	}
	existingQuestions := make([]string, 0, len(project.Questions)+len(req.Items))
	for _, question := range project.Questions {
		existingQuestions = append(existingQuestions, question.Text)
	}
	seen := map[string]struct{}{}
	for _, item := range req.Items {
		if _, duplicate := seen[item.CandidateID]; duplicate {
			return nil, nil, fmt.Errorf("候选 %s 重复提交", item.CandidateID)
		}
		seen[item.CandidateID] = struct{}{}
		index, ok := candidateIndex[item.CandidateID]
		if !ok {
			return nil, nil, fmt.Errorf("候选 %s 不存在", item.CandidateID)
		}
		candidate := &job.Candidates[index]
		if candidate.ReviewStatus != "pending" {
			return nil, nil, fmt.Errorf("候选 %s 已经审核", item.CandidateID)
		}
		switch item.Action {
		case "reject":
			candidate.ReviewStatus = "rejected"
		case "approve":
			candidate.Question = strings.TrimSpace(item.Question)
			candidate.Answer = strings.TrimSpace(item.Answer)
			candidate.AnswerKeyPoints = cleanStrings(item.AnswerKeyPoints)
			candidate.Category = strings.TrimSpace(item.Category)
			candidate.Difficulty = strings.TrimSpace(item.Difficulty)
			candidate.Tags = cleanStrings(item.Tags)
			candidate.EvidenceQuotes = cleanStrings(item.EvidenceQuotes)
			passage, ok := passageByID[candidate.PassageIDs[0]]
			if !ok {
				return nil, nil, fmt.Errorf("候选 %s 的来源语料已不存在", item.CandidateID)
			}
			candidate.Issues = validateGenerationCandidate(*candidate, passage, existingQuestions, nil)
			for _, issue := range candidate.Issues {
				if issue.Severity == "error" {
					return nil, nil, fmt.Errorf("候选 %s 未通过校验：%s", item.CandidateID, issue.Message)
				}
			}
			answerable := candidate.Answerable
			question := Question{
				ID: project.NextQuestionID, Text: candidate.Question, Answer: candidate.Answer,
				RelevantPassageIDs: append([]int64(nil), candidate.PassageIDs...), Category: candidate.Category,
				Difficulty: candidate.Difficulty, Tags: append([]string(nil), candidate.Tags...), ReviewState: "approved",
				AnswerKeyPoints: append([]string(nil), candidate.AnswerKeyPoints...), Answerable: &answerable,
				DatasetVersion: project.Version, AnnotationSource: fmt.Sprintf("ai:%s:%s", job.Model, job.PromptVersion),
			}
			project.Questions = append(project.Questions, question)
			project.NextQuestionID++
			candidate.ReviewStatus = "approved"
			candidate.ApprovedQuestionID = question.ID
			existingQuestions = append(existingQuestions, question.Text)
		default:
			return nil, nil, fmt.Errorf("候选 %s 的操作只能是 approve 或 reject", item.CandidateID)
		}
	}
	if err := s.save(projectID, project); err != nil {
		return nil, nil, err
	}
	job.UpdatedAt = s.now().UTC()
	if err := s.writeGenerationJob(job); err != nil {
		return nil, nil, err
	}
	return job, project, nil
}
