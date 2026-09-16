package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type baseline struct {
	RequireSuccess  bool               `json:"require_success"`
	RequireComplete bool               `json:"require_complete"`
	Minimum         map[string]float64 `json:"minimum"`
	Maximum         map[string]float64 `json:"maximum"`
	MaximumDecrease map[string]float64 `json:"maximum_decrease"`
	MaximumIncrease map[string]float64 `json:"maximum_increase"`
}

type check struct {
	Path      string   `json:"path"`
	Rule      string   `json:"rule"`
	Expected  float64  `json:"expected"`
	Actual    float64  `json:"actual"`
	Reference *float64 `json:"reference,omitempty"`
	Delta     *float64 `json:"delta,omitempty"`
	Passed    bool     `json:"passed"`
	Error     string   `json:"error,omitempty"`
}

type report struct {
	Passed bool    `json:"passed"`
	Checks []check `json:"checks"`
}

func main() {
	resultPath := flag.String("result", "", "evaluation result JSON file")
	localURL := flag.String("local-url", "", "optional loopback WeKnora URL for an end-to-end evaluation")
	datasetID := flag.String("dataset-id", "default", "dataset ID for a local evaluation")
	knowledgeBaseID := flag.String("knowledge-base-id", "", "reference knowledge base ID")
	chatModelID := flag.String("chat-model-id", "", "chat model ID")
	rerankModelID := flag.String("rerank-model-id", "", "rerank model ID")
	apiKeyEnv := flag.String("api-key-env", "WEKNORA_EVAL_API_KEY", "environment variable containing the local API key")
	pollInterval := flag.Duration("poll-interval", 2*time.Second, "local evaluation polling interval")
	timeout := flag.Duration("timeout", 30*time.Minute, "local evaluation timeout")
	resultOut := flag.String("result-out", "", "optional raw local evaluation response path")
	baselinePath := flag.String("baseline", "evaluation/baseline.json", "baseline threshold JSON file")
	referenceResultPath := flag.String("reference-result", "", "reference evaluation result JSON for relative checks")
	reportPath := flag.String("report", "", "optional report JSON output file")
	flag.Parse()
	if (*resultPath == "") == (*localURL == "") {
		fmt.Fprintln(os.Stderr, "provide exactly one of -result or -local-url")
		os.Exit(2)
	}

	var result map[string]any
	var err error
	if *localURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		result, err = runLocalEvaluation(ctx, localEvaluationOptions{
			BaseURL: *localURL, APIKey: os.Getenv(*apiKeyEnv), DatasetID: *datasetID,
			KnowledgeBaseID: *knowledgeBaseID, ChatModelID: *chatModelID,
			RerankModelID: *rerankModelID, PollInterval: *pollInterval,
		})
		if err == nil && *resultOut != "" {
			err = writeJSON(*resultOut, result)
		}
	} else {
		result, err = readJSONMap(*resultPath)
	}
	if err != nil {
		fatal(err)
	}
	configBytes, err := os.ReadFile(*baselinePath)
	if err != nil {
		fatal(err)
	}
	var config baseline
	if err := json.Unmarshal(configBytes, &config); err != nil {
		fatal(fmt.Errorf("parse baseline: %w", err))
	}
	if err := validateBaseline(config); err != nil {
		fatal(err)
	}
	var reference map[string]any
	if *referenceResultPath != "" {
		reference, err = readJSONMap(*referenceResultPath)
		if err != nil {
			fatal(fmt.Errorf("read reference result: %w", err))
		}
		reference = unwrapAPIData(reference)
	} else if len(config.MaximumDecrease) > 0 || len(config.MaximumIncrease) > 0 {
		fatal(errors.New("-reference-result is required when relative checks are configured"))
	}

	report := evaluate(unwrapAPIData(result), reference, config)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(encoded))
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
	if !report.Passed {
		os.Exit(1)
	}
}

type localEvaluationOptions struct {
	BaseURL, APIKey, DatasetID, KnowledgeBaseID, ChatModelID, RerankModelID string
	PollInterval                                                            time.Duration
	Client                                                                  *http.Client
}

func runLocalEvaluation(ctx context.Context, options localEvaluationOptions) (map[string]any, error) {
	base, err := url.Parse(strings.TrimRight(options.BaseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || !isLoopbackHost(base.Hostname()) {
		return nil, fmt.Errorf("-local-url must use localhost or a loopback IP, got %q", options.BaseURL)
	}
	if options.PollInterval <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	payload := map[string]string{
		"dataset_id": options.DatasetID, "knowledge_base_id": options.KnowledgeBaseID,
		"chat_id": options.ChatModelID, "rerank_id": options.RerankModelID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(options.BaseURL, "/") + "/api/v1/evaluation"
	created, err := requestEvaluationJSON(ctx, client, http.MethodPost, endpoint, options.APIKey, encoded)
	if err != nil {
		return nil, fmt.Errorf("start evaluation: %w", err)
	}
	taskID, ok := lookup(unwrapAPIData(created), "task.id")
	taskIDString, stringOK := taskID.(string)
	if !ok || !stringOK || taskIDString == "" {
		return nil, errors.New("start evaluation response is missing a valid data.task.id")
	}

	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()
	for {
		query := endpoint + "?task_id=" + url.QueryEscape(taskIDString)
		current, requestErr := requestEvaluationJSON(ctx, client, http.MethodGet, query, options.APIKey, nil)
		if requestErr != nil {
			return nil, fmt.Errorf("poll evaluation: %w", requestErr)
		}
		status, statusOK := lookup(unwrapAPIData(current), "task.status")
		if statusOK && (isSuccessStatus(status) || isFailureStatus(status)) {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("evaluation did not finish: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestEvaluationJSON(
	ctx context.Context, client *http.Client, method, endpoint, apiKey string, body []byte,
) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

func isFailureStatus(value any) bool {
	switch status := value.(type) {
	case float64:
		return status == 3
	case string:
		return strings.EqualFold(status, "failed") || strings.EqualFold(status, "failure")
	default:
		return false
	}
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func evaluate(result, reference map[string]any, config baseline) report {
	report := report{Passed: true}
	if config.RequireSuccess {
		actual, ok := lookup(result, "task.status")
		passed := ok && isSuccessStatus(actual)
		report.Checks = append(report.Checks, check{
			Path: "task.status", Rule: "success", Passed: passed,
			Error: missingError(ok),
		})
		report.Passed = report.Passed && passed
	}
	if config.RequireComplete {
		finished, finishedOK := numberAt(result, "task.finished")
		total, totalOK := numberAt(result, "task.total")
		passed := finishedOK && totalOK && total > 0 && finished == total
		report.Checks = append(report.Checks, check{
			Path: "task.finished", Rule: "equals task.total", Expected: total,
			Actual: finished, Passed: passed, Error: missingError(finishedOK && totalOK),
		})
		report.Passed = report.Passed && passed
	}
	appendThresholdChecks := func(values map[string]float64, rule string) {
		paths := make([]string, 0, len(values))
		for path := range values {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			expected := values[path]
			actual, ok := numberAt(result, path)
			passed := ok && ((rule == "minimum" && actual >= expected) ||
				(rule == "maximum" && actual <= expected))
			report.Checks = append(report.Checks, check{
				Path: path, Rule: rule, Expected: expected, Actual: actual,
				Passed: passed, Error: missingError(ok),
			})
			report.Passed = report.Passed && passed
		}
	}
	appendThresholdChecks(config.Minimum, "minimum")
	appendThresholdChecks(config.Maximum, "maximum")
	appendRelativeChecks := func(values map[string]float64, rule string) {
		paths := make([]string, 0, len(values))
		for path := range values {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			allowedChange := values[path]
			actual, actualOK := numberAt(result, path)
			referenceValue, referenceOK := numberAt(reference, path)
			var referencePointer, deltaPointer *float64
			passed := false
			if actualOK && referenceOK {
				delta := actual - referenceValue
				referencePointer, deltaPointer = &referenceValue, &delta
				passed = (rule == "maximum_decrease" && delta >= -allowedChange) ||
					(rule == "maximum_increase" && delta <= allowedChange)
			}
			report.Checks = append(report.Checks, check{
				Path: path, Rule: rule, Expected: allowedChange, Actual: actual,
				Reference: referencePointer, Delta: deltaPointer, Passed: passed,
				Error: relativeMissingError(actualOK, referenceOK),
			})
			report.Passed = report.Passed && passed
		}
	}
	appendRelativeChecks(config.MaximumDecrease, "maximum_decrease")
	appendRelativeChecks(config.MaximumIncrease, "maximum_increase")
	return report
}

func validateBaseline(config baseline) error {
	for rule, values := range map[string]map[string]float64{
		"maximum_decrease": config.MaximumDecrease,
		"maximum_increase": config.MaximumIncrease,
	} {
		for path, value := range values {
			if value < 0 {
				return fmt.Errorf("baseline %s for %s must not be negative", rule, path)
			}
		}
	}
	return nil
}

func unwrapAPIData(value map[string]any) map[string]any {
	if data, ok := value["data"].(map[string]any); ok {
		return data
	}
	return value
}

func lookup(root map[string]any, path string) (any, bool) {
	var current any = root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func numberAt(root map[string]any, path string) (float64, bool) {
	if root == nil {
		return 0, false
	}
	value, ok := lookup(root, path)
	if !ok {
		return 0, false
	}
	number, ok := value.(float64)
	return number, ok
}

func relativeMissingError(actualOK, referenceOK bool) string {
	switch {
	case !actualOK && !referenceOK:
		return "candidate and reference values are missing or not numeric"
	case !actualOK:
		return "candidate value is missing or not numeric"
	case !referenceOK:
		return "reference value is missing or not numeric"
	default:
		return ""
	}
}

func isSuccessStatus(value any) bool {
	switch status := value.(type) {
	case float64:
		return status == 2
	case string:
		return strings.EqualFold(status, "success") || strings.EqualFold(status, "succeeded")
	default:
		return false
	}
}

func missingError(ok bool) string {
	if ok {
		return ""
	}
	return "value is missing or not numeric"
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}
	if result == nil {
		return nil, errors.New("result must be a JSON object")
	}
	return result, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
