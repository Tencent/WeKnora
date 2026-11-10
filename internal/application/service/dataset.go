package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/parquet-go/parquet-go"
)

// DatasetService provides operations for working with datasets
type DatasetService struct{}

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

// GetDatasetByID retrieves QA pairs from dataset by ID
func (d *DatasetService) GetDatasetByID(ctx context.Context, datasetID string) ([]*types.QAPair, error) {
	logger.Info(ctx, "Start getting dataset by ID")
	logger.Infof(ctx, "Getting dataset with ID: %s", datasetID)

	dataset := DefaultDataset()
	dataset.PrintStats(ctx)
	qaPairs := dataset.Iterate()

	logger.Infof(ctx, "Retrieved %d QA pairs from dataset", len(qaPairs))
	return qaPairs, nil
}

// DefaultDataset loads and initializes the default dataset from parquet files
func DefaultDataset() dataset {
	datasetDir := "./dataset/samples"
	queries, err := loadParquet[TextInfo](fmt.Sprintf("%s/queries.parquet", datasetDir))
	if err != nil {
		panic(err)
	}
	corpus, err := loadParquet[TextInfo](fmt.Sprintf("%s/corpus.parquet", datasetDir))
	if err != nil {
		panic(err)
	}
	answers, err := loadParquet[TextInfo](fmt.Sprintf("%s/answers.parquet", datasetDir))
	if err != nil {
		panic(err)
	}
	qrels, err := loadParquet[RelsInfo](fmt.Sprintf("%s/qrels.parquet", datasetDir))
	if err != nil {
		panic(err)
	}
	qas, err := loadParquet[QaInfo](fmt.Sprintf("%s/qas.parquet", datasetDir))
	if err != nil {
		panic(err)
	}

	res := dataset{
		queries: make(map[int64]string),  // qid -> question text
		corpus:  make(map[int64]string),  // pid -> passage text
		answers: make(map[int64]string),  // aid -> answer text
		qrels:   make(map[int64][]int64), // qid -> list of pid
		qas:     make(map[int64]int64),   // qid -> aid
	}
	for _, qi := range queries {
		res.queries[qi.ID] = qi.Text
	}
	for _, ci := range corpus {
		res.corpus[ci.ID] = ci.Text
	}
	for _, ai := range answers {
		res.answers[ai.ID] = ai.Text
	}
	for _, ri := range qrels {
		res.qrels[ri.QID] = append(res.qrels[ri.QID], ri.PID)
	}
	for _, qi := range qas {
		res.qas[qi.QID] = qi.AID
	}
	return res
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

	for qid, question := range d.queries {
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
