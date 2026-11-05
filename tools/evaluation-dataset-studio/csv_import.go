package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type csvImportRequest struct {
	Kind string `json:"kind"`
	CSV  string `json:"csv"`
}

type CSVImportSummary struct {
	Kind          string `json:"kind"`
	ImportedCount int    `json:"imported_count"`
}

func importProjectCSV(project *DatasetProject, req csvImportRequest) (CSVImportSummary, error) {
	if project == nil {
		return CSVImportSummary{}, errors.New("数据集项目不能为空")
	}
	if strings.TrimSpace(req.CSV) == "" {
		return CSVImportSummary{}, errors.New("CSV 内容不能为空")
	}

	reader := csv.NewReader(strings.NewReader(req.CSV))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return CSVImportSummary{}, fmt.Errorf("解析 CSV 失败：%w", err)
	}
	if len(records) < 2 {
		return CSVImportSummary{}, errors.New("CSV 至少需要表头和一行数据")
	}
	header, err := csvHeader(records[0])
	if err != nil {
		return CSVImportSummary{}, err
	}

	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case "passages":
		count, err := importPassageRecords(project, header, records[1:])
		return CSVImportSummary{Kind: "passages", ImportedCount: count}, err
	case "questions":
		count, err := importQuestionRecords(project, header, records[1:])
		return CSVImportSummary{Kind: "questions", ImportedCount: count}, err
	default:
		return CSVImportSummary{}, errors.New("kind 必须为 passages 或 questions")
	}
}

func csvHeader(record []string) (map[string]int, error) {
	header := make(map[string]int, len(record))
	for i, value := range record {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "\ufeff")))
		if key == "" {
			continue
		}
		if _, exists := header[key]; exists {
			return nil, fmt.Errorf("CSV 表头 %q 重复", value)
		}
		header[key] = i
	}
	return header, nil
}

func importPassageRecords(project *DatasetProject, header map[string]int, records [][]string) (int, error) {
	textColumn, ok := findCSVColumn(header, "text", "文本", "语料")
	if !ok {
		return 0, errors.New("语料 CSV 缺少 text（文本）列")
	}
	used := make(map[int64]struct{}, len(project.Passages))
	for _, passage := range project.Passages {
		used[passage.ID] = struct{}{}
	}
	nextID := project.NextPassageID
	items := make([]Passage, 0, len(records))
	for i, record := range records {
		row := i + 2
		if csvRecordEmpty(record) {
			continue
		}
		text := strings.TrimSpace(csvValue(record, textColumn))
		if text == "" {
			return 0, fmt.Errorf("CSV 第 %d 行：语料文本不能为空", row)
		}
		id, err := csvRecordID(record, header, []string{"id", "语料id"}, &nextID, used)
		if err != nil {
			return 0, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		items = append(items, Passage{
			ID:          id,
			Text:        text,
			Source:      csvNamedValue(record, header, "source", "来源"),
			Tags:        splitCSVList(csvNamedValue(record, header, "tags", "标签")),
			ReviewState: csvNamedValue(record, header, "review_state", "审核状态"),
		})
	}
	if len(items) == 0 {
		return 0, errors.New("CSV 没有可导入的语料行")
	}
	project.Passages = append(project.Passages, items...)
	project.NextPassageID = nextID
	return len(items), nil
}

func importQuestionRecords(project *DatasetProject, header map[string]int, records [][]string) (int, error) {
	textColumn, hasText := findCSVColumn(header, "text", "问题")
	answerColumn, hasAnswer := findCSVColumn(header, "answer", "标准答案", "答案")
	relationColumn, hasRelations := findCSVColumn(header, "relevant_passage_ids", "相关语料id", "相关语料ids")
	if !hasText || !hasAnswer || !hasRelations {
		return 0, errors.New("问题 CSV 必须包含 text、answer、relevant_passage_ids 列")
	}
	passageIDs := make(map[int64]struct{}, len(project.Passages))
	for _, passage := range project.Passages {
		passageIDs[passage.ID] = struct{}{}
	}
	used := make(map[int64]struct{}, len(project.Questions))
	for _, question := range project.Questions {
		used[question.ID] = struct{}{}
	}
	nextID := project.NextQuestionID
	items := make([]Question, 0, len(records))
	for i, record := range records {
		row := i + 2
		if csvRecordEmpty(record) {
			continue
		}
		text := strings.TrimSpace(csvValue(record, textColumn))
		answer := strings.TrimSpace(csvValue(record, answerColumn))
		if text == "" || answer == "" {
			return 0, fmt.Errorf("CSV 第 %d 行：问题和标准答案不能为空", row)
		}
		relations, err := parsePassageIDs(csvValue(record, relationColumn), passageIDs)
		if err != nil {
			return 0, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		id, err := csvRecordID(record, header, []string{"id", "问题id"}, &nextID, used)
		if err != nil {
			return 0, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		answerable, err := parseOptionalCSVBool(csvNamedValue(record, header, "answerable", "是否可回答"))
		if err != nil {
			return 0, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		filters, err := parseCSVKeyValueMap(csvNamedValue(record, header, "retrieval_filters", "检索过滤条件"))
		if err != nil {
			return 0, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		items = append(items, Question{
			ID:                 id,
			Text:               text,
			Answer:             answer,
			RelevantPassageIDs: relations,
			Category:           csvNamedValue(record, header, "category", "分类", "问题类别"),
			Difficulty:         csvNamedValue(record, header, "difficulty", "难度", "难度等级"),
			Tags:               splitCSVList(csvNamedValue(record, header, "tags", "标签")),
			ReviewState:        csvNamedValue(record, header, "review_state", "审核状态"),
			AnswerKeyPoints:    splitCSVList(csvNamedValue(record, header, "answer_key_points", "答案核心要点")),
			Answerable:         answerable,
			ExpectedDocuments:  splitCSVList(csvNamedValue(record, header, "expected_documents", "期望召回文档", "召回文档")),
			ForbiddenDocuments: splitCSVList(csvNamedValue(record, header, "forbidden_documents", "禁止召回文档")),
			TestRole:           csvNamedValue(record, header, "test_role", "测试权限角色"),
			RetrievalFilters:   filters,
			DatasetVersion:     csvNamedValue(record, header, "dataset_version", "测试集版本"),
			AnnotationSource:   csvNamedValue(record, header, "annotation_source", "标记来源"),
		})
	}
	if len(items) == 0 {
		return 0, errors.New("CSV 没有可导入的问题行")
	}
	project.Questions = append(project.Questions, items...)
	project.NextQuestionID = nextID
	return len(items), nil
}

func parseOptionalCSVBool(value string) (*bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return nil, nil
	}
	var result bool
	switch value {
	case "true", "1", "yes", "是", "可回答":
		result = true
	case "false", "0", "no", "否", "不可回答":
		result = false
	default:
		return nil, fmt.Errorf("是否可回答 %q 必须是 true/false、是/否或可回答/不可回答", value)
	}
	return &result, nil
}

func parseCSVKeyValueMap(value string) (map[string]string, error) {
	parts := splitCSVRawList(value)
	if len(parts) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(parts))
	for _, part := range parts {
		key, item, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		item = strings.TrimSpace(item)
		if !ok || key == "" || item == "" {
			return nil, fmt.Errorf("检索过滤条件 %q 必须使用 key=value 格式", part)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("检索过滤条件键 %q 重复", key)
		}
		result[key] = item
	}
	return result, nil
}

func csvRecordID(record []string, header map[string]int, names []string, nextID *int64, used map[int64]struct{}) (int64, error) {
	value := csvNamedValue(record, header, names...)
	if value != "" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 0 {
			return 0, fmt.Errorf("ID %q 必须是非负整数", value)
		}
		if _, exists := used[id]; exists {
			return 0, fmt.Errorf("ID %d 已存在或在 CSV 中重复", id)
		}
		used[id] = struct{}{}
		if id >= *nextID {
			*nextID = id + 1
		}
		return id, nil
	}
	for {
		if _, exists := used[*nextID]; !exists {
			id := *nextID
			used[id] = struct{}{}
			*nextID++
			return id, nil
		}
		*nextID++
	}
}

func parsePassageIDs(value string, existing map[int64]struct{}) ([]int64, error) {
	parts := splitCSVRawList(value)
	if len(parts) == 0 {
		return nil, errors.New("相关语料 ID 不能为空")
	}
	result := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("相关语料 ID %q 不是非负整数", part)
		}
		if _, ok := existing[id]; !ok {
			return nil, fmt.Errorf("相关语料 ID %d 不存在", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("相关语料 ID %d 重复", id)
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func findCSVColumn(header map[string]int, names ...string) (int, bool) {
	for _, name := range names {
		if column, ok := header[strings.ToLower(name)]; ok {
			return column, true
		}
	}
	return 0, false
}

func csvNamedValue(record []string, header map[string]int, names ...string) string {
	if column, ok := findCSVColumn(header, names...); ok {
		return strings.TrimSpace(csvValue(record, column))
	}
	return ""
}

func csvValue(record []string, column int) string {
	if column < 0 || column >= len(record) {
		return ""
	}
	return record[column]
}

func csvRecordEmpty(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func splitCSVList(value string) []string {
	parts := splitCSVRawList(value)
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func splitCSVRawList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '|', ';', '；', ',', '，':
			return true
		default:
			return false
		}
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}
	return result
}
