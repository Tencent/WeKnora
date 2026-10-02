package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

const maxImportedParquetBytes = 128 << 20

type weKnoraImportRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	ZIPBase64 string `json:"zip_base64"`
}

type WeKnoraImportSummary struct {
	QuestionCount int `json:"question_count"`
	PassageCount  int `json:"passage_count"`
	AnswerCount   int `json:"answer_count"`
	QrelCount     int `json:"qrel_count"`
}

func (s *projectStore) importWeKnora(req weKnoraImportRequest) (*DatasetProject, WeKnoraImportSummary, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Version = strings.TrimSpace(req.Version)
	if err := validateDatasetID(req.ID); err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	if req.Name == "" {
		return nil, WeKnoraImportSummary{}, errors.New("数据集名称不能为空")
	}
	if req.Version == "" {
		req.Version = "0.1.0"
	}
	zipBytes, err := base64.StdEncoding.DecodeString(req.ZIPBase64)
	if err != nil {
		return nil, WeKnoraImportSummary{}, fmt.Errorf("ZIP Base64 无效：%w", err)
	}
	if len(zipBytes) == 0 {
		return nil, WeKnoraImportSummary{}, errors.New("ZIP 文件不能为空")
	}

	tempDir, err := os.MkdirTemp(s.root, ".import-weknora-*")
	if err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	defer os.RemoveAll(tempDir)
	if err := extractWeKnoraZIP(zipBytes, tempDir); err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	rows, err := readWeKnoraRows(tempDir)
	if err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	project, err := projectFromWeKnoraRows(req, rows, s.now())
	if err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	if report := validateProject(project); !report.Valid {
		return nil, WeKnoraImportSummary{}, &validationFailedError{Report: report}
	}
	if err := s.importProject(project); err != nil {
		return nil, WeKnoraImportSummary{}, err
	}
	return project, WeKnoraImportSummary{
		QuestionCount: len(rows.Queries),
		PassageCount:  len(rows.Corpus),
		AnswerCount:   len(rows.Answers),
		QrelCount:     len(rows.Qrels),
	}, nil
}

func extractWeKnoraZIP(data []byte, targetDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("打开 ZIP 失败：%w", err)
	}
	expected := make(map[string]struct{}, len(exportFileNames))
	for _, name := range exportFileNames {
		expected[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(exportFileNames))
	var total uint64
	for _, file := range reader.File {
		if _, ok := expected[file.Name]; !ok || filepath.Base(file.Name) != file.Name {
			return fmt.Errorf("ZIP 包含非预期文件：%s", file.Name)
		}
		if _, duplicate := seen[file.Name]; duplicate {
			return fmt.Errorf("ZIP 文件重复：%s", file.Name)
		}
		seen[file.Name] = struct{}{}
		total += file.UncompressedSize64
		if total > maxImportedParquetBytes {
			return fmt.Errorf("ZIP 解压后超过 %d MiB 限制", maxImportedParquetBytes>>20)
		}
		input, err := file.Open()
		if err != nil {
			return fmt.Errorf("打开 %s 失败：%w", file.Name, err)
		}
		output, err := os.OpenFile(filepath.Join(targetDir, file.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, int64(file.UncompressedSize64)+1))
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil {
			return fmt.Errorf("解压 %s 失败：%w", file.Name, copyErr)
		}
		if closeOutputErr != nil || closeInputErr != nil {
			return fmt.Errorf("关闭 %s 失败", file.Name)
		}
	}
	for _, name := range exportFileNames {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("ZIP 缺少必需文件：%s", name)
		}
	}
	return nil
}

func readWeKnoraRows(dir string) (exportRows, error) {
	for _, item := range []struct {
		name   string
		fields map[string]reflect.Type
	}{
		{"queries.parquet", map[string]reflect.Type{"id": reflect.TypeOf(int64(0)), "text": reflect.TypeOf("")}},
		{"corpus.parquet", map[string]reflect.Type{"id": reflect.TypeOf(int64(0)), "text": reflect.TypeOf("")}},
		{"answers.parquet", map[string]reflect.Type{"id": reflect.TypeOf(int64(0)), "text": reflect.TypeOf("")}},
		{"qrels.parquet", map[string]reflect.Type{"qid": reflect.TypeOf(int64(0)), "pid": reflect.TypeOf(int64(0))}},
		{"qas.parquet", map[string]reflect.Type{"qid": reflect.TypeOf(int64(0)), "aid": reflect.TypeOf(int64(0))}},
	} {
		if err := verifyParquetSchema(filepath.Join(dir, item.name), item.fields); err != nil {
			return exportRows{}, fmt.Errorf("校验 %s 失败：%w", item.name, err)
		}
	}
	queries, err := parquet.ReadFile[TextRow](filepath.Join(dir, "queries.parquet"))
	if err != nil {
		return exportRows{}, fmt.Errorf("读取 queries.parquet 失败：%w", err)
	}
	corpus, err := parquet.ReadFile[TextRow](filepath.Join(dir, "corpus.parquet"))
	if err != nil {
		return exportRows{}, fmt.Errorf("读取 corpus.parquet 失败：%w", err)
	}
	answers, err := parquet.ReadFile[TextRow](filepath.Join(dir, "answers.parquet"))
	if err != nil {
		return exportRows{}, fmt.Errorf("读取 answers.parquet 失败：%w", err)
	}
	qrels, err := parquet.ReadFile[QrelRow](filepath.Join(dir, "qrels.parquet"))
	if err != nil {
		return exportRows{}, fmt.Errorf("读取 qrels.parquet 失败：%w", err)
	}
	qas, err := parquet.ReadFile[QARow](filepath.Join(dir, "qas.parquet"))
	if err != nil {
		return exportRows{}, fmt.Errorf("读取 qas.parquet 失败：%w", err)
	}
	return exportRows{Queries: queries, Corpus: corpus, Answers: answers, Qrels: qrels, QAs: qas}, nil
}

func verifyParquetSchema(path string, expected map[string]reflect.Type) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		return err
	}
	columns := parquetFile.Schema().Columns()
	if len(columns) != len(expected) {
		return fmt.Errorf("字段数量为 %d，期望 %d", len(columns), len(expected))
	}
	for name, wantType := range expected {
		leaf, ok := parquetFile.Schema().Lookup(name)
		if !ok {
			return fmt.Errorf("缺少字段 %s", name)
		}
		if wantType == reflect.TypeOf("") {
			if leaf.Node.Type().String() != "STRING" {
				return fmt.Errorf("字段 %s 类型为 %s，期望 STRING", name, leaf.Node.Type())
			}
			continue
		}
		if gotType := leaf.Node.GoType(); gotType != wantType {
			return fmt.Errorf("字段 %s 类型为 %s，期望 %s", name, gotType, wantType)
		}
	}
	return nil
}

func projectFromWeKnoraRows(req weKnoraImportRequest, rows exportRows, now time.Time) (*DatasetProject, error) {
	queries, err := uniqueTextRows("query", rows.Queries)
	if err != nil {
		return nil, err
	}
	corpus, err := uniqueTextRows("corpus", rows.Corpus)
	if err != nil {
		return nil, err
	}
	answers, err := uniqueTextRows("answer", rows.Answers)
	if err != nil {
		return nil, err
	}
	qaMap := make(map[int64]int64, len(rows.QAs))
	usedAnswers := make(map[int64]struct{}, len(rows.QAs))
	for _, row := range rows.QAs {
		if _, ok := queries[row.QID]; !ok {
			return nil, fmt.Errorf("qas 引用了不存在的问题 ID %d", row.QID)
		}
		if _, ok := answers[row.AID]; !ok {
			return nil, fmt.Errorf("qas 引用了不存在的答案 ID %d", row.AID)
		}
		if _, duplicate := qaMap[row.QID]; duplicate {
			return nil, fmt.Errorf("问题 ID %d 存在多个 qas 映射，当前工具只支持一问一答", row.QID)
		}
		qaMap[row.QID] = row.AID
		usedAnswers[row.AID] = struct{}{}
	}
	if len(usedAnswers) != len(answers) {
		return nil, errors.New("answers.parquet 中存在未被 qas 引用的答案，无法无损导入")
	}

	relations := make(map[int64][]int64, len(queries))
	seenQrels := make(map[[2]int64]struct{}, len(rows.Qrels))
	for _, row := range rows.Qrels {
		if _, ok := queries[row.QID]; !ok {
			return nil, fmt.Errorf("qrels 引用了不存在的问题 ID %d", row.QID)
		}
		if _, ok := corpus[row.PID]; !ok {
			return nil, fmt.Errorf("qrels 引用了不存在的语料 ID %d", row.PID)
		}
		key := [2]int64{row.QID, row.PID}
		if _, duplicate := seenQrels[key]; duplicate {
			return nil, fmt.Errorf("qrels 关系重复：qid=%d, pid=%d", row.QID, row.PID)
		}
		seenQrels[key] = struct{}{}
		relations[row.QID] = append(relations[row.QID], row.PID)
	}

	project := newDatasetProject(createDatasetRequest{ID: req.ID, Name: req.Name, Version: req.Version}, now)
	passageIDs := make([]int64, 0, len(corpus))
	for id := range corpus {
		passageIDs = append(passageIDs, id)
	}
	sort.Slice(passageIDs, func(i, j int) bool { return passageIDs[i] < passageIDs[j] })
	for _, id := range passageIDs {
		project.Passages = append(project.Passages, Passage{ID: id, Text: corpus[id]})
	}
	questionIDs := make([]int64, 0, len(queries))
	for id := range queries {
		questionIDs = append(questionIDs, id)
	}
	sort.Slice(questionIDs, func(i, j int) bool { return questionIDs[i] < questionIDs[j] })
	for _, id := range questionIDs {
		aid, ok := qaMap[id]
		if !ok {
			return nil, fmt.Errorf("问题 ID %d 缺少 qas 答案映射", id)
		}
		pids := relations[id]
		if len(pids) == 0 {
			return nil, fmt.Errorf("问题 ID %d 缺少 qrels 语料关系", id)
		}
		sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
		project.Questions = append(project.Questions, Question{
			ID:                 id,
			Text:               queries[id],
			Answer:             answers[aid],
			RelevantPassageIDs: pids,
		})
	}
	normalizeProject(project)
	return project, nil
}

func uniqueTextRows(kind string, rows []TextRow) (map[int64]string, error) {
	result := make(map[int64]string, len(rows))
	for _, row := range rows {
		if row.ID < 0 {
			return nil, fmt.Errorf("%s ID %d 不能为负数", kind, row.ID)
		}
		if _, duplicate := result[row.ID]; duplicate {
			return nil, fmt.Errorf("%s ID %d 重复", kind, row.ID)
		}
		text := strings.TrimSpace(row.Text)
		if text == "" {
			return nil, fmt.Errorf("%s ID %d 的文本为空", kind, row.ID)
		}
		result[row.ID] = text
	}
	return result, nil
}
