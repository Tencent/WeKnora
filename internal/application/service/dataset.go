package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/parquet-go/parquet-go"
)

// DatasetService provides operations for working with datasets
type DatasetService struct {
	rootDir string
}

const (
	datasetManifestSchemaVersion = 1
	defaultDatasetID             = "default"
)

var datasetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// DatasetManifest describes the provenance and intended coverage of a dataset.
// The evaluation result stores a fingerprint of the loaded rows separately, so
// metadata and exact evaluated content are both auditable.
type DatasetManifest struct {
	SchemaVersion      int      `json:"schema_version"`
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	Language           string   `json:"language,omitempty"`
	Scenario           string   `json:"scenario,omitempty"`
	Source             string   `json:"source,omitempty"`
	License            string   `json:"license,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	CoverageDimensions []string `json:"coverage_dimensions,omitempty"`
}

var requiredDatasetFiles = [...]string{
	"queries.parquet", "corpus.parquet", "answers.parquet", "qrels.parquet", "qas.parquet",
}

// NewDatasetService creates a new DatasetService instance
func NewDatasetService() interfaces.DatasetService {
	rootDir := strings.TrimSpace(os.Getenv("WEKNORA_EVALUATION_DATASET_DIR"))
	if rootDir == "" {
		rootDir = "./dataset"
	}
	return &DatasetService{rootDir: rootDir}
}

// TextInfo represents text data with ID in parquet format
type TextInfo struct {
	ID   int64  `parquet:"id"`   // Unique identifier
	Text string `parquet:"text"` // Text content
}

// RelsInfo represents question-passage relations in parquet format
type RelsInfo struct {
	QID int64 `parquet:"qid"` // Question ID
	PID int64 `parquet:"pid"` // Passage ID
}

// QaInfo represents question-answer relations in parquet format
type QaInfo struct {
	QID int64 `parquet:"qid"` // Question ID
	AID int64 `parquet:"aid"` // Answer ID
}

// GetDatasetByID retrieves QA pairs from dataset by ID
func (d *DatasetService) GetDatasetByID(ctx context.Context, datasetID string) ([]*types.QAPair, error) {
	logger.Info(ctx, "Start getting dataset by ID")
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		datasetID = defaultDatasetID
	}
	logger.Infof(ctx, "Getting dataset with ID: %s", datasetID)

	datasetDir, err := d.datasetDirectory(datasetID)
	if err != nil {
		return nil, err
	}
	manifest, err := loadDatasetManifest(datasetDir, datasetID)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Loading evaluation dataset %q (%s)", manifest.Name, manifest.ID)
	dataset, err := loadDatasetFromDir(datasetDir)
	if err != nil {
		return nil, err
	}
	dataset.PrintStats(ctx)
	qaPairs := dataset.Iterate()

	logger.Infof(ctx, "Retrieved %d QA pairs from dataset", len(qaPairs))
	return qaPairs, nil
}

// ListDatasets discovers manifests without loading large Parquet bodies. Full
// schema and relationship validation is intentionally repeated when a run
// starts so files changed after listing cannot bypass validation.
func (d *DatasetService) ListDatasets(ctx context.Context) ([]types.EvaluationDataset, error) {
	entries, err := os.ReadDir(d.rootDir)
	if err != nil {
		return nil, fmt.Errorf("read evaluation dataset directory %s: %w", d.rootDir, err)
	}
	result := make([]types.EvaluationDataset, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		datasetID := entry.Name()
		if datasetID == "samples" {
			datasetID = defaultDatasetID
		} else if !datasetIDPattern.MatchString(datasetID) {
			continue
		}
		datasetDir := filepath.Join(d.rootDir, entry.Name())
		manifest, manifestErr := loadDatasetManifest(datasetDir, datasetID)
		item := types.EvaluationDataset{ID: datasetID, Name: datasetID}
		if manifestErr == nil {
			item = manifest.asEvaluationDataset()
			manifestErr = checkDatasetFiles(datasetDir)
		}
		item.Available = manifestErr == nil
		if manifestErr != nil {
			item.ValidationError = manifestErr.Error()
			logger.Warnf(ctx, "Evaluation dataset %s is unavailable: %v", datasetID, manifestErr)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == "default" {
			return true
		}
		if result[j].ID == "default" {
			return false
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (m *DatasetManifest) asEvaluationDataset() types.EvaluationDataset {
	return types.EvaluationDataset{
		SchemaVersion: m.SchemaVersion, ID: m.ID, Name: m.Name, Description: m.Description,
		Language: m.Language, Scenario: m.Scenario, Source: m.Source, License: m.License,
		CreatedAt: m.CreatedAt, CoverageDimensions: append([]string(nil), m.CoverageDimensions...),
	}
}

func checkDatasetFiles(datasetDir string) error {
	for _, name := range requiredDatasetFiles {
		path := filepath.Join(datasetDir, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("required dataset file %s is not readable: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required dataset file %s is not a regular file", name)
		}
	}
	return nil
}

// DefaultDataset loads and initializes the default dataset from parquet files.
// It returns errors instead of panicking so malformed local data cannot
// terminate the server process.
func DefaultDataset() (*dataset, error) {
	rootDir := strings.TrimSpace(os.Getenv("WEKNORA_EVALUATION_DATASET_DIR"))
	if rootDir == "" {
		rootDir = "./dataset"
	}
	datasetDir := filepath.Join(rootDir, "samples")
	result, err := loadDatasetFromDir(datasetDir)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (d *DatasetService) datasetDirectory(datasetID string) (string, error) {
	datasetID = strings.TrimSpace(datasetID)
	if !datasetIDPattern.MatchString(datasetID) {
		return "", fmt.Errorf(
			"invalid dataset ID %q: use 1-64 letters, digits, dots, underscores, or hyphens", datasetID,
		)
	}
	if datasetID == defaultDatasetID {
		return filepath.Join(d.rootDir, "samples"), nil
	}
	return filepath.Join(d.rootDir, datasetID), nil
}

func loadDatasetManifest(datasetDir, expectedID string) (*DatasetManifest, error) {
	manifestPath := filepath.Join(datasetDir, "manifest.json")
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read dataset manifest %s: %w", manifestPath, err)
	}
	var manifest DatasetManifest
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse dataset manifest %s: %w", manifestPath, err)
	}
	if manifest.SchemaVersion != datasetManifestSchemaVersion {
		return nil, fmt.Errorf(
			"dataset manifest %s has schema_version %d, expected %d",
			manifestPath, manifest.SchemaVersion, datasetManifestSchemaVersion,
		)
	}
	if manifest.ID != expectedID {
		return nil, fmt.Errorf("dataset manifest ID %q does not match requested ID %q", manifest.ID, expectedID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("dataset manifest %s must contain a non-empty name", manifestPath)
	}
	return &manifest, nil
}

func loadDatasetFromDir(datasetDir string) (dataset, error) {
	queries, err := loadParquet[TextInfo](fmt.Sprintf("%s/queries.parquet", datasetDir))
	if err != nil {
		return dataset{}, fmt.Errorf("load queries.parquet: %w", err)
	}
	corpus, err := loadParquet[TextInfo](fmt.Sprintf("%s/corpus.parquet", datasetDir))
	if err != nil {
		return dataset{}, fmt.Errorf("load corpus.parquet: %w", err)
	}
	answers, err := loadParquet[TextInfo](fmt.Sprintf("%s/answers.parquet", datasetDir))
	if err != nil {
		return dataset{}, fmt.Errorf("load answers.parquet: %w", err)
	}
	qrels, err := loadParquet[RelsInfo](fmt.Sprintf("%s/qrels.parquet", datasetDir))
	if err != nil {
		return dataset{}, fmt.Errorf("load qrels.parquet: %w", err)
	}
	qas, err := loadParquet[QaInfo](fmt.Sprintf("%s/qas.parquet", datasetDir))
	if err != nil {
		return dataset{}, fmt.Errorf("load qas.parquet: %w", err)
	}

	res := dataset{
		queries: make(map[int64]string),  // qid -> question text
		corpus:  make(map[int64]string),  // pid -> passage text
		answers: make(map[int64]string),  // aid -> answer text
		qrels:   make(map[int64][]int64), // qid -> list of pid
		qas:     make(map[int64]int64),   // qid -> aid
	}
	for _, qi := range queries {
		if strings.TrimSpace(qi.Text) == "" {
			return dataset{}, fmt.Errorf("queries.parquet contains empty text for id %d", qi.ID)
		}
		if _, exists := res.queries[qi.ID]; exists {
			return dataset{}, fmt.Errorf("queries.parquet contains duplicate id %d", qi.ID)
		}
		res.queries[qi.ID] = qi.Text
	}
	for _, ci := range corpus {
		if strings.TrimSpace(ci.Text) == "" {
			return dataset{}, fmt.Errorf("corpus.parquet contains empty text for id %d", ci.ID)
		}
		if _, exists := res.corpus[ci.ID]; exists {
			return dataset{}, fmt.Errorf("corpus.parquet contains duplicate id %d", ci.ID)
		}
		res.corpus[ci.ID] = ci.Text
	}
	for _, ai := range answers {
		if strings.TrimSpace(ai.Text) == "" {
			return dataset{}, fmt.Errorf("answers.parquet contains empty text for id %d", ai.ID)
		}
		if _, exists := res.answers[ai.ID]; exists {
			return dataset{}, fmt.Errorf("answers.parquet contains duplicate id %d", ai.ID)
		}
		res.answers[ai.ID] = ai.Text
	}
	seenQrels := make(map[[2]int64]struct{}, len(qrels))
	for _, ri := range qrels {
		if _, exists := res.queries[ri.QID]; !exists {
			return dataset{}, fmt.Errorf("qrels.parquet qid %d is missing from queries.parquet", ri.QID)
		}
		if _, exists := res.corpus[ri.PID]; !exists {
			return dataset{}, fmt.Errorf("qrels.parquet pid %d is missing from corpus.parquet", ri.PID)
		}
		relation := [2]int64{ri.QID, ri.PID}
		if _, exists := seenQrels[relation]; exists {
			return dataset{}, fmt.Errorf("qrels.parquet contains duplicate qid/pid relation %d/%d", ri.QID, ri.PID)
		}
		seenQrels[relation] = struct{}{}
		res.qrels[ri.QID] = append(res.qrels[ri.QID], ri.PID)
	}
	for _, qi := range qas {
		if _, exists := res.queries[qi.QID]; !exists {
			return dataset{}, fmt.Errorf("qas.parquet qid %d is missing from queries.parquet", qi.QID)
		}
		if _, exists := res.answers[qi.AID]; !exists {
			return dataset{}, fmt.Errorf("qas.parquet aid %d is missing from answers.parquet", qi.AID)
		}
		if _, exists := res.qas[qi.QID]; exists {
			return dataset{}, fmt.Errorf("qas.parquet contains duplicate qid %d", qi.QID)
		}
		res.qas[qi.QID] = qi.AID
	}
	if len(res.queries) == 0 {
		return dataset{}, errors.New("dataset must contain at least one query")
	}
	for qid := range res.queries {
		if len(res.qrels[qid]) == 0 {
			return dataset{}, fmt.Errorf("query %d has no qrels passage", qid)
		}
		if _, exists := res.qas[qid]; !exists {
			return dataset{}, fmt.Errorf("query %d has no qas answer", qid)
		}
	}
	return res, nil
}

// dataset represents the in-memory dataset structure
type dataset struct {
	queries map[int64]string  // qid -> question text
	corpus  map[int64]string  // pid -> passage text
	answers map[int64]string  // aid -> answer text
	qrels   map[int64][]int64 // qid -> list of related pids
	qas     map[int64]int64   // qid -> aid
}

// Iterate generates QA pairs from the dataset
func (d *dataset) Iterate() []*types.QAPair {
	qids := make([]int64, 0, len(d.queries))
	for qid := range d.queries {
		qids = append(qids, qid)
	}
	sort.Slice(qids, func(i, j int) bool { return qids[i] < qids[j] })
	pairs := make([]*types.QAPair, 0, len(qids))

	for _, qid := range qids {
		question := d.queries[qid]
		// Get answer info
		aid, hasAnswer := d.qas[qid]
		answer := ""
		if hasAnswer {
			answer = d.answers[aid]
		}

		// Get related passages
		pids := d.qrels[qid]
		var pidStr []int
		for _, pid := range pids {
			pidStr = append(pidStr, int(pid))
		}
		var passages []string
		for _, pid := range pids {
			passages = append(passages, d.corpus[pid])
		}

		pairs = append(pairs, &types.QAPair{
			QID:      int(qid),
			Question: question,
			PIDs:     pidStr,
			Passages: passages,
			AID:      int(aid),
			Answer:   answer,
		})
	}

	return pairs
}

// GetContextForQID retrieves context passages for a given question ID
func (d *dataset) GetContextForQID(qid int64) ([]string, error) {
	pids, ok := d.qrels[qid]
	if !ok {
		return nil, errors.New("question ID not found")
	}

	var contextParts []string
	for _, pid := range pids {
		if text, exists := d.corpus[pid]; exists {
			contextParts = append(contextParts, text)
		}
	}

	return contextParts, nil
}

// PrintStats prints dataset statistics to the logger
func (d *dataset) PrintStats(ctx context.Context) {
	logger.Infof(ctx, "QA System Statistics:")
	logger.Infof(ctx, "- Total queries: %d", len(d.queries))
	logger.Infof(ctx, "- Total corpus passages: %d", len(d.corpus))
	logger.Infof(ctx, "- Total answers: %d", len(d.answers))

	// Calculate average passages per query
	totalRelations := 0
	for _, pids := range d.qrels {
		totalRelations += len(pids)
	}
	avgPassages := float64(totalRelations) / float64(len(d.qrels))
	logger.Infof(ctx, "- Average passages per query: %.2f", avgPassages)

	// Calculate coverage
	coveredQueries := len(d.qas)
	coverage := float64(coveredQueries) / float64(len(d.queries)) * 100
	logger.Infof(ctx, "- Answer coverage: %.2f%% (%d/%d)", coverage, coveredQueries, len(d.queries))
}

// PrintRandomQA prints a random question with its related passages and answer
func (d *dataset) PrintRandomQA() error {
	// Get a random qid
	var qid int64
	for k := range d.qas {
		qid = k
		break
	}
	if qid == 0 {
		return errors.New("no questions available")
	}

	// Get question text
	question, ok := d.queries[qid]
	if !ok {
		return fmt.Errorf("question %d not found", qid)
	}

	// Get answer info
	aid, ok := d.qas[qid]
	if !ok {
		return fmt.Errorf("answer for question %d not found", qid)
	}
	answer, ok := d.answers[aid]
	if !ok {
		return fmt.Errorf("answer %d not found", aid)
	}

	// Print formatted QA
	fmt.Println("===== Random QA =====")
	fmt.Printf("QID: %d\n", qid)
	fmt.Printf("Question: %s\n", question)

	// Print passages if available
	if pids, exists := d.qrels[qid]; exists && len(pids) > 0 {
		fmt.Println("\nRelated passages:")
		for i, pid := range pids {
			if text, exists := d.corpus[pid]; exists {
				fmt.Printf("\nPassage %d (PID: %d):\n%s\n", i+1, pid, text)
			}
		}
	} else {
		fmt.Println("\nNo related passages found")
	}

	// Print answer
	fmt.Printf("\nAnswer (AID: %d):\n%s\n", aid, answer)

	return nil
}

// loadParquet loads data from parquet file into specified type
func loadParquet[T any](filePath string) ([]T, error) {
	rows, err := parquet.ReadFile[T](filePath)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
