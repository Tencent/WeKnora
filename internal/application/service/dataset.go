package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/parquet-go/parquet-go"
)

// DatasetService provides operations for working with datasets
type DatasetService struct{ sampleDir string }

var (
	ErrDatasetUnknown       = errors.New("unsupported dataset ID: supported IDs are default and sample")
	ErrDatasetInvalid       = errors.New("invalid evaluation dataset")
	ErrDatasetResourceLimit = errors.New("evaluation dataset resource limit exceeded")
)

type datasetLimits struct {
	maxFileBytes      int64
	maxDecodedBytes   int64
	maxRows           int64
	maxID             int64
	maxTextBytes      int
	maxTotalTextBytes int
}

// maxID bounds evaluation.prepare's maxPID+1 slice to <=100001 strings
// (about 1.6 MiB of string headers on ARM64), independently of sparse IDs.
var defaultDatasetLimits = datasetLimits{
	maxFileBytes: 8 << 20, maxDecodedBytes: 32 << 20,
	maxRows: 10000, maxID: 100000, maxTextBytes: 64 << 10,
	maxTotalTextBytes: 32 << 20,
}

// NewDatasetService creates a new DatasetService instance
func NewDatasetService() interfaces.DatasetService {
	return &DatasetService{}
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

// GetDatasetByID accepts only the bundled, public sample aliases. It does not
// claim that caller-provided business/hidden datasets are registered.
func (d *DatasetService) GetDatasetByID(ctx context.Context, datasetID string) ([]*types.QAPair, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if datasetID != "default" && datasetID != "sample" {
		return nil, ErrDatasetUnknown
	}
	dir := d.sampleDir
	if dir == "" {
		dir = "./dataset/samples"
	}
	loaded, err := loadDataset(dir, defaultDatasetLimits)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loaded.PrintStats(ctx)
	return loaded.Iterate(), nil
}

// DefaultDataset returns errors instead of panicking on missing/malformed files.
func DefaultDataset() (dataset, error) {
	return loadDataset("./dataset/samples", defaultDatasetLimits)
}

func loadDataset(dir string, limits datasetLimits) (dataset, error) {
	queries, err := loadBoundedParquet[TextInfo](filepath.Join(dir, "queries.parquet"), limits)
	if err != nil {
		return dataset{}, err
	}
	corpus, err := loadBoundedParquet[TextInfo](filepath.Join(dir, "corpus.parquet"), limits)
	if err != nil {
		return dataset{}, err
	}
	answers, err := loadBoundedParquet[TextInfo](filepath.Join(dir, "answers.parquet"), limits)
	if err != nil {
		return dataset{}, err
	}
	qrels, err := loadBoundedParquet[RelsInfo](filepath.Join(dir, "qrels.parquet"), limits)
	if err != nil {
		return dataset{}, err
	}
	qas, err := loadBoundedParquet[QaInfo](filepath.Join(dir, "qas.parquet"), limits)
	if err != nil {
		return dataset{}, err
	}
	res := dataset{queries: map[int64]string{}, corpus: map[int64]string{},
		answers: map[int64]string{}, qrels: map[int64][]int64{}, qas: map[int64]int64{}}
	totalText := 0
	for _, group := range []struct {
		name string
		rows []TextInfo
		dest map[int64]string
	}{
		{"queries", queries, res.queries}, {"corpus", corpus, res.corpus}, {"answers", answers, res.answers},
	} {
		if len(group.rows) == 0 {
			return dataset{}, fmt.Errorf("%w: empty %s", ErrDatasetInvalid, group.name)
		}
		for _, row := range group.rows {
			if row.ID < 0 {
				return dataset{}, fmt.Errorf("%w: negative %s ID", ErrDatasetInvalid, group.name)
			}
			if row.ID > limits.maxID {
				return dataset{}, fmt.Errorf("%w: %s ID outside [0,%d]", ErrDatasetResourceLimit, group.name, limits.maxID)
			}
			if _, exists := group.dest[row.ID]; exists {
				return dataset{}, fmt.Errorf("%w: duplicate %s ID", ErrDatasetInvalid, group.name)
			}
			if strings.TrimSpace(row.Text) == "" {
				return dataset{}, fmt.Errorf("%w: empty %s text", ErrDatasetInvalid, group.name)
			}
			if len(row.Text) > limits.maxTextBytes || len(row.Text) > limits.maxTotalTextBytes-totalText {
				return dataset{}, fmt.Errorf("%w: text size", ErrDatasetResourceLimit)
			}
			totalText += len(row.Text)
			group.dest[row.ID] = row.Text
		}
	}
	seenRelations := map[RelsInfo]bool{}
	for _, rel := range qrels {
		if _, ok := res.queries[rel.QID]; !ok {
			return dataset{}, fmt.Errorf("%w: qrel references missing query", ErrDatasetInvalid)
		}
		if _, ok := res.corpus[rel.PID]; !ok {
			return dataset{}, fmt.Errorf("%w: qrel references missing passage", ErrDatasetInvalid)
		}
		if seenRelations[rel] {
			return dataset{}, fmt.Errorf("%w: duplicate qrel", ErrDatasetInvalid)
		}
		seenRelations[rel] = true
		res.qrels[rel.QID] = append(res.qrels[rel.QID], rel.PID)
	}
	for _, qa := range qas {
		if _, ok := res.queries[qa.QID]; !ok {
			return dataset{}, fmt.Errorf("%w: answer mapping references missing query", ErrDatasetInvalid)
		}
		if _, ok := res.answers[qa.AID]; !ok {
			return dataset{}, fmt.Errorf("%w: answer mapping references missing answer", ErrDatasetInvalid)
		}
		if _, ok := res.qas[qa.QID]; ok {
			return dataset{}, fmt.Errorf("%w: multiple answer mappings for query", ErrDatasetInvalid)
		}
		res.qas[qa.QID] = qa.AID
	}
	for qid := range res.queries {
		if _, ok := res.qas[qid]; !ok {
			return dataset{}, fmt.Errorf("%w: query has no answer", ErrDatasetInvalid)
		}
		if len(res.qrels[qid]) == 0 {
			return dataset{}, fmt.Errorf("%w: query has no related passage", ErrDatasetInvalid)
		}
		sort.Slice(res.qrels[qid], func(i, j int) bool { return res.qrels[qid][i] < res.qrels[qid][j] })
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
	var pairs []*types.QAPair

	qids := make([]int64, 0, len(d.queries))
	for qid := range d.queries {
		qids = append(qids, qid)
	}
	sort.Slice(qids, func(i, j int) bool { return qids[i] < qids[j] })
	for _, qid := range qids {
		question := d.queries[qid]
		// Get answer info
		aid, hasAnswer := d.qas[qid]
		answer := ""
		if hasAnswer {
			answer = d.answers[aid]
		}

		// Get related passages
		pids := append([]int64(nil), d.qrels[qid]...)
		sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
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
	avgPassages := 0.0
	if len(d.qrels) > 0 {
		avgPassages = float64(totalRelations) / float64(len(d.qrels))
	}
	logger.Infof(ctx, "- Average passages per query: %.2f", avgPassages)

	// Calculate coverage
	coveredQueries := len(d.qas)
	coverage := 0.0
	if len(d.queries) > 0 {
		coverage = float64(coveredQueries) / float64(len(d.queries)) * 100
	}
	logger.Infof(ctx, "- Answer coverage: %.2f%% (%d/%d)", coverage, coveredQueries, len(d.queries))
}

// PrintRandomQA prints a random question with its related passages and answer
func (d *dataset) PrintRandomQA() error {
	// Get a random qid
	var qid int64
	found := false
	for k := range d.qas {
		qid = k
		found = true
		break
	}
	if !found {
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

// loadParquet retains the internal helper with fixed bounds.
func loadParquet[T any](filePath string) ([]T, error) {
	return loadBoundedParquet[T](filePath, defaultDatasetLimits)
}

// The external decoder can panic on unsupported schemas. Contain that decoder
// boundary and return an error; never leave a partially usable dataset.
func loadBoundedParquet[T any](filePath string, limits datasetLimits) (rows []T, err error) {
	defer func() {
		if recover() != nil {
			rows = nil
			err = fmt.Errorf("%w: parquet decoder rejected %s", ErrDatasetInvalid, filepath.Base(filePath))
		}
	}()
	stat, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("dataset %s: %w", filepath.Base(filePath), err)
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: parquet file must be regular", ErrDatasetInvalid)
	}
	if stat.Size() > limits.maxFileBytes {
		return nil, fmt.Errorf("%w: parquet file size", ErrDatasetResourceLimit)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	parsed, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDatasetInvalid, filepath.Base(filePath), err)
	}
	if parsed.NumRows() < 0 || parsed.NumRows() > limits.maxRows {
		return nil, fmt.Errorf("%w: parquet row count", ErrDatasetResourceLimit)
	}
	expected := parquet.SchemaOf(new(T))
	for _, path := range expected.Columns() {
		want, _ := expected.Lookup(path...)
		got, ok := parsed.Schema().Lookup(path...)
		// Bundled IDs use both INT64 and decimal STRING. Decode explicitly;
		// generic physical conversion is not decimal parsing.
		compatible := ok && (got.Node.Type().Kind() == want.Node.Type().Kind() ||
			(want.Node.Type().Kind() == parquet.Int64 && got.Node.Type().Kind() == parquet.ByteArray))
		if !compatible || got.MaxRepetitionLevel != 0 {
			return nil, fmt.Errorf("%w: missing/incompatible required parquet column %s", ErrDatasetInvalid, strings.Join(path, "."))
		}
	}
	var totalBytes, totalRows int64
	for _, group := range parsed.Metadata().RowGroups {
		if group.NumRows < 0 || group.NumRows > limits.maxRows-totalRows {
			return nil, fmt.Errorf("%w: row group count", ErrDatasetResourceLimit)
		}
		totalRows += group.NumRows
		for _, column := range group.Columns {
			if column.FilePath != "" || column.MetaData.NumValues != group.NumRows {
				return nil, fmt.Errorf("%w: external/inconsistent parquet column", ErrDatasetInvalid)
			}
			n := column.MetaData.TotalUncompressedSize
			if n < 0 || n > limits.maxDecodedBytes-totalBytes {
				return nil, fmt.Errorf("%w: declared uncompressed parquet size", ErrDatasetResourceLimit)
			}
			totalBytes += n
		}
	}
	if totalRows != parsed.NumRows() {
		return nil, fmt.Errorf("%w: inconsistent parquet row counts", ErrDatasetInvalid)
	}
	reader := parquet.NewReader(parsed)
	defer reader.Close()
	batch := make([]parquet.Row, 32)
	for {
		n, readErr := reader.ReadRows(batch)
		if n < 0 || n > len(batch) {
			return nil, fmt.Errorf("%w: invalid parquet reader count", ErrDatasetInvalid)
		}
		if int64(len(rows))+int64(n) > limits.maxRows {
			return nil, fmt.Errorf("%w: decoded row count", ErrDatasetResourceLimit)
		}
		for _, raw := range batch[:n] {
			var decoded T
			target := reflect.ValueOf(&decoded).Elem()
			for i := 0; i < target.NumField(); i++ {
				path := strings.Split(target.Type().Field(i).Tag.Get("parquet"), ",")[0]
				leaf, _ := parsed.Schema().Lookup(path)
				found := false
				for _, value := range raw {
					if value.Column() != leaf.ColumnIndex {
						continue
					}
					if found || value.IsNull() {
						return nil, fmt.Errorf("%w: null/duplicate required value", ErrDatasetInvalid)
					}
					found = true
					field := target.Field(i)
					if field.Kind() == reflect.Int64 {
						var id int64
						if value.Kind() == parquet.Int64 {
							id = value.Int64()
						} else if value.Kind() == parquet.ByteArray {
							text := string(value.ByteArray())
							if text == "" || strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
								return nil, fmt.Errorf("%w: invalid decimal ID", ErrDatasetInvalid)
							}
							var parseErr error
							id, parseErr = strconv.ParseInt(text, 10, 64)
							if parseErr != nil {
								return nil, fmt.Errorf("%w: invalid decimal ID", ErrDatasetInvalid)
							}
						}
						if id < 0 {
							return nil, fmt.Errorf("%w: negative ID", ErrDatasetInvalid)
						}
						if id > limits.maxID {
							return nil, fmt.Errorf("%w: ID outside [0,%d]", ErrDatasetResourceLimit, limits.maxID)
						}
						field.SetInt(id)
					} else {
						field.SetString(string(value.ByteArray()))
					}
				}
				if !found {
					return nil, fmt.Errorf("%w: missing required value", ErrDatasetInvalid)
				}
			}
			rows = append(rows, decoded)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("%w: parquet rows: %v", ErrDatasetInvalid, readErr)
		}
		if n == 0 {
			return nil, fmt.Errorf("%w: parquet reader made no progress", ErrDatasetInvalid)
		}
	}
	if int64(len(rows)) != parsed.NumRows() {
		return nil, fmt.Errorf("%w: decoded row count mismatch", ErrDatasetInvalid)
	}
	return rows, nil
}
