package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
)

var exportFilePartPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const weKnoraFiveParquetProfileID = "weknora-five-parquet-v1"

type ExportContractProfile struct {
	ID              string                    `json:"id"`
	Description     string                    `json:"description"`
	StrictRootFiles bool                      `json:"strict_root_files"`
	Files           []ExportContractFileShape `json:"files"`
}

type ExportContractFileShape struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

var weKnoraFiveParquetProfile = ExportContractProfile{
	ID:              weKnoraFiveParquetProfileID,
	Description:     "WeKnora 当前五文件 Parquet 评测数据集契约",
	StrictRootFiles: true,
	Files: []ExportContractFileShape{
		{Name: "queries.parquet", Fields: []string{"id:int64", "text:string"}},
		{Name: "corpus.parquet", Fields: []string{"id:int64", "text:string"}},
		{Name: "answers.parquet", Fields: []string{"id:int64", "text:string"}},
		{Name: "qrels.parquet", Fields: []string{"qid:int64", "pid:int64"}},
		{Name: "qas.parquet", Fields: []string{"qid:int64", "aid:int64"}},
	},
}

var exportFileNames = []string{
	"queries.parquet",
	"corpus.parquet",
	"answers.parquet",
	"qrels.parquet",
	"qas.parquet",
}

type exportRows struct {
	Queries []TextRow
	Corpus  []TextRow
	Answers []TextRow
	Qrels   []QrelRow
	QAs     []QARow
}

type validationFailedError struct {
	Report ValidationReport
}

func (e *validationFailedError) Error() string {
	return "数据集存在阻断错误，无法导出"
}

func buildExportRows(project *DatasetProject) (exportRows, error) {
	if report := validateProject(project); !report.Valid {
		return exportRows{}, &validationFailedError{Report: report}
	}

	passages := append([]Passage(nil), project.Passages...)
	sort.Slice(passages, func(i, j int) bool { return passages[i].ID < passages[j].ID })
	pidMap := make(map[int64]int64, len(passages))
	rows := exportRows{
		Queries: make([]TextRow, 0, len(project.Questions)),
		Corpus:  make([]TextRow, 0, len(passages)),
		Answers: make([]TextRow, 0, len(project.Questions)),
		Qrels:   []QrelRow{},
		QAs:     make([]QARow, 0, len(project.Questions)),
	}
	for index, passage := range passages {
		exportID := int64(index)
		pidMap[passage.ID] = exportID
		rows.Corpus = append(rows.Corpus, TextRow{ID: exportID, Text: strings.TrimSpace(passage.Text)})
	}

	questions := append([]Question(nil), project.Questions...)
	sort.Slice(questions, func(i, j int) bool { return questions[i].ID < questions[j].ID })
	for _, question := range questions {
		rows.Queries = append(rows.Queries, TextRow{ID: question.ID, Text: strings.TrimSpace(question.Text)})
		rows.Answers = append(rows.Answers, TextRow{ID: question.ID, Text: strings.TrimSpace(question.Answer)})
		rows.QAs = append(rows.QAs, QARow{QID: question.ID, AID: question.ID})
		pids := append([]int64(nil), question.RelevantPassageIDs...)
		sort.Slice(pids, func(i, j int) bool { return pidMap[pids[i]] < pidMap[pids[j]] })
		for _, internalPID := range pids {
			exportPID, ok := pidMap[internalPID]
			if !ok {
				return exportRows{}, fmt.Errorf("question %d references missing passage %d", question.ID, internalPID)
			}
			rows.Qrels = append(rows.Qrels, QrelRow{QID: question.ID, PID: exportPID})
		}
	}
	return rows, nil
}

func (s *projectStore) exportWeKnora(project *DatasetProject) (string, ExportHistoryItem, error) {
	rows, err := buildExportRows(project)
	if err != nil {
		return "", ExportHistoryItem{}, err
	}

	tempDir, err := os.MkdirTemp(s.exports, ".export-*")
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	defer os.RemoveAll(tempDir)

	files := map[string]func(string) error{
		"queries.parquet": func(path string) error { return parquet.WriteFile(path, rows.Queries) },
		"corpus.parquet":  func(path string) error { return parquet.WriteFile(path, rows.Corpus) },
		"answers.parquet": func(path string) error { return parquet.WriteFile(path, rows.Answers) },
		"qrels.parquet":   func(path string) error { return parquet.WriteFile(path, rows.Qrels) },
		"qas.parquet":     func(path string) error { return parquet.WriteFile(path, rows.QAs) },
	}
	for _, name := range exportFileNames {
		if err := files[name](filepath.Join(tempDir, name)); err != nil {
			return "", ExportHistoryItem{}, fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := verifyParquetFiles(tempDir, rows); err != nil {
		return "", ExportHistoryItem{}, err
	}

	fileName := exportFileName(project)
	tempZip, err := os.CreateTemp(s.exports, ".zip-*")
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	tempZipPath := tempZip.Name()
	defer os.Remove(tempZipPath)
	if err := writeZip(tempZip, tempDir); err != nil {
		tempZip.Close()
		return "", ExportHistoryItem{}, err
	}
	if err := tempZip.Close(); err != nil {
		return "", ExportHistoryItem{}, err
	}
	info, err := os.Stat(tempZipPath)
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	createdAt := s.now().UTC()
	zipSHA256, err := sha256File(tempZipPath)
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	item := ExportHistoryItem{
		ID:                historyID(createdAt),
		FileName:          fileName,
		CreatedAt:         createdAt,
		DatasetCreatedAt:  project.CreatedAt,
		Size:              info.Size(),
		SHA256:            zipSHA256,
		ContractProfileID: weKnoraFiveParquetProfileID,
		QuestionCount:     len(rows.Queries),
		PassageCount:      len(rows.Corpus),
		QrelCount:         len(rows.Qrels),
		QuestionIDs:       make([]int64, 0, len(rows.Queries)),
		PassageIDMap:      make(map[int64]int64, len(project.Passages)),
		DeploymentStatus:  exportDeploymentNotDeployed,
	}
	for _, query := range rows.Queries {
		item.QuestionIDs = append(item.QuestionIDs, query.ID)
	}
	passages := append([]Passage(nil), project.Passages...)
	sort.Slice(passages, func(i, j int) bool { return passages[i].ID < passages[j].ID })
	for exportPID, passage := range passages {
		item.PassageIDMap[int64(exportPID)] = passage.ID
	}
	metadata, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	metadata = append(metadata, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.exports, project.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", ExportHistoryItem{}, err
	}
	finalPath := filepath.Join(dir, item.ID+".zip")
	if _, err := os.Stat(finalPath); err == nil {
		return "", ExportHistoryItem{}, errors.New("同一时刻的导出记录已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", ExportHistoryItem{}, err
	}
	if err := os.Rename(tempZipPath, finalPath); err != nil {
		return "", ExportHistoryItem{}, err
	}
	if err := writeBytesAtomic(filepath.Join(dir, item.ID+".json"), metadata, 0o640); err != nil {
		_ = os.Remove(finalPath)
		return "", ExportHistoryItem{}, err
	}
	return finalPath, item, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func verifyParquetFiles(dir string, expected exportRows) error {
	queries, err := parquet.ReadFile[TextRow](filepath.Join(dir, "queries.parquet"))
	if err != nil {
		return fmt.Errorf("verify queries.parquet: %w", err)
	}
	corpus, err := parquet.ReadFile[TextRow](filepath.Join(dir, "corpus.parquet"))
	if err != nil {
		return fmt.Errorf("verify corpus.parquet: %w", err)
	}
	answers, err := parquet.ReadFile[TextRow](filepath.Join(dir, "answers.parquet"))
	if err != nil {
		return fmt.Errorf("verify answers.parquet: %w", err)
	}
	qrels, err := parquet.ReadFile[QrelRow](filepath.Join(dir, "qrels.parquet"))
	if err != nil {
		return fmt.Errorf("verify qrels.parquet: %w", err)
	}
	qas, err := parquet.ReadFile[QARow](filepath.Join(dir, "qas.parquet"))
	if err != nil {
		return fmt.Errorf("verify qas.parquet: %w", err)
	}
	if !reflect.DeepEqual(queries, expected.Queries) ||
		!reflect.DeepEqual(corpus, expected.Corpus) ||
		!reflect.DeepEqual(answers, expected.Answers) ||
		!reflect.DeepEqual(qrels, expected.Qrels) ||
		!reflect.DeepEqual(qas, expected.QAs) {
		return errors.New("Parquet 回读结果与导出数据不一致")
	}
	return nil
}

func writeZip(target *os.File, sourceDir string) error {
	writer := zip.NewWriter(target)
	for _, name := range exportFileNames {
		input, err := os.Open(filepath.Join(sourceDir, name))
		if err != nil {
			writer.Close()
			return err
		}
		entry, err := writer.Create(name)
		if err != nil {
			input.Close()
			writer.Close()
			return err
		}
		_, copyErr := io.Copy(entry, input)
		closeErr := input.Close()
		if copyErr != nil {
			writer.Close()
			return copyErr
		}
		if closeErr != nil {
			writer.Close()
			return closeErr
		}
	}
	return writer.Close()
}

func exportFileName(project *DatasetProject) string {
	version := exportFilePartPattern.ReplaceAllString(strings.TrimSpace(project.Version), "-")
	version = strings.Trim(version, "-.")
	if version == "" {
		version = "unversioned"
	}
	return fmt.Sprintf("%s-%s-weknora.zip", project.ID, version)
}
