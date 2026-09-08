package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
)

func TestDatasetServiceGetDatasetByID(t *testing.T) {
	service := &DatasetService{datasetRoot: filepath.Join("..", "..", "..", "dataset")}

	t.Run("default", func(t *testing.T) {
		dataset, err := service.GetDatasetByID(context.Background(), "default")
		require.NoError(t, err)
		require.NotEmpty(t, dataset.Corpus)
		require.NotEmpty(t, dataset.QAPairs)
	})

	t.Run("benchmark v1", func(t *testing.T) {
		dataset, err := service.GetDatasetByID(context.Background(), "benchmark_v1")
		require.NoError(t, err)
		require.Len(t, dataset.Corpus, 32)
		require.Len(t, dataset.QAPairs, 15)
		require.Equal(t, 32, dataset.Identity.CorpusCount)
		require.Equal(t, 15, dataset.Identity.QuestionCount)
		require.Equal(t, 15, dataset.Identity.QrelsCount)
		require.Equal(t, 15, dataset.Identity.AnswerCount)
		require.Len(t, dataset.Identity.DatasetSemanticSHA256, 64)
		for index, passage := range dataset.Corpus {
			require.Equal(t, index, passage.PID, "benchmark corpus must preserve PID order")
			require.NotEmpty(t, passage.Text)
		}
		for index, pair := range dataset.QAPairs {
			require.Equal(t, index, pair.QID, "benchmark iteration must be deterministic by QID")
			require.NotEmpty(t, pair.Question)
			require.NotEmpty(t, pair.Answer)
			require.NotEmpty(t, pair.PIDs)
		}

		// Query 0 marks PID 1 as a hard negative in benchmark.json. It must
		// exist in the candidate corpus without becoming qrels ground truth.
		require.NotContains(t, dataset.QAPairs[0].PIDs, 1)
		require.Equal(t, 1, dataset.Corpus[1].PID)
		require.NotEmpty(t, dataset.Corpus[1].Text)
	})

	for _, datasetID := range []string{"not-exist", "../samples", "benchmark_v1/../samples", ""} {
		t.Run("reject "+datasetID, func(t *testing.T) {
			_, err := service.GetDatasetByID(context.Background(), datasetID)
			require.ErrorContains(t, err, "unsupported dataset ID")
		})
	}
}

func TestDatasetSemanticIdentityDeterministicAndContentSensitive(t *testing.T) {
	build := func(reverse bool) dataset {
		result := dataset{
			queries: make(map[int64]string), corpus: make(map[int64]string), answers: make(map[int64]string),
			qrels: make(map[int64][]int64), qas: make(map[int64]int64),
		}
		if reverse {
			result.corpus[2], result.corpus[1] = "two", "one"
			result.queries[2], result.queries[1] = "q2", "q1"
			result.answers[2], result.answers[1] = "a2", "a1"
			result.qrels[2], result.qrels[1] = []int64{2}, []int64{2, 1}
			result.qas[2], result.qas[1] = 2, 1
		} else {
			result.corpus[1], result.corpus[2] = "one", "two"
			result.queries[1], result.queries[2] = "q1", "q2"
			result.answers[1], result.answers[2] = "a1", "a2"
			result.qrels[1], result.qrels[2] = []int64{1, 2}, []int64{2}
			result.qas[1], result.qas[2] = 1, 2
		}
		return result
	}

	firstDataset := build(false)
	first, err := firstDataset.describe("same-id")
	require.NoError(t, err)
	secondDataset := build(true)
	second, err := secondDataset.describe("same-id")
	require.NoError(t, err)
	require.Equal(t, first.Identity.DatasetSemanticSHA256, second.Identity.DatasetSemanticSHA256)

	changedCorpus := build(false)
	changedCorpus.corpus[1] = "changed"
	corpusIdentity, err := changedCorpus.describe("same-id")
	require.NoError(t, err)
	require.NotEqual(t, first.Identity.DatasetSemanticSHA256, corpusIdentity.Identity.DatasetSemanticSHA256)

	changedQrels := build(false)
	changedQrels.qrels[1] = []int64{1}
	qrelsIdentity, err := changedQrels.describe("same-id")
	require.NoError(t, err)
	require.NotEqual(t, first.Identity.DatasetSemanticSHA256, qrelsIdentity.Identity.DatasetSemanticSHA256)
}

func TestDatasetServiceReturnsLoadError(t *testing.T) {
	service := &DatasetService{datasetRoot: t.TempDir()}

	_, err := service.GetDatasetByID(context.Background(), "default")
	require.ErrorContains(t, err, `load dataset "default"`)
	require.ErrorContains(t, err, "load queries")
}

func validIntegrityDataset() dataset {
	return dataset{
		queries: map[int64]string{1: "question"},
		corpus:  map[int64]string{10: "passage one", 11: "passage two"},
		answers: map[int64]string{20: "answer"},
		qrels:   map[int64][]int64{1: {10, 11}},
		qas:     map[int64]int64{1: 20},
	}
}

func TestDatasetIntegrityRejectsMalformedSemanticData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dataset)
		want   string
	}{
		{name: "query without qrels", mutate: func(d *dataset) { delete(d.qrels, 1) }, want: "query 1 has no qrels"},
		{name: "query without qas", mutate: func(d *dataset) { delete(d.qas, 1) }, want: "query 1 has no qas relation"},
		{
			name:   "qrels unknown query",
			mutate: func(d *dataset) { d.qrels[2] = []int64{10} },
			want:   "qrels references unknown query 2",
		},
		{
			name:   "qrel unknown passage",
			mutate: func(d *dataset) { d.qrels[1] = []int64{99} },
			want:   "references unknown corpus passage",
		},
		{name: "qas unknown query", mutate: func(d *dataset) { d.qas[2] = 20 }, want: "qas references unknown query 2"},
		{name: "qas unknown answer", mutate: func(d *dataset) { d.qas[1] = 99 }, want: "references unknown answer"},
		{name: "empty question", mutate: func(d *dataset) { d.queries[1] = " \n" }, want: "query 1 has empty text"},
		{name: "empty corpus", mutate: func(d *dataset) {
			d.corpus[10] = "\t"
		}, want: "corpus passage 10 has empty text"},
		{name: "empty answer", mutate: func(d *dataset) { d.answers[20] = " " }, want: "answer 20 has empty text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataset := validIntegrityDataset()
			tc.mutate(&dataset)
			_, err := dataset.describe("malformed")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDatasetIntegrityAllowsMultipleDistinctQrelsAndPreservesText(t *testing.T) {
	dataset := validIntegrityDataset()
	dataset.queries[1] = "  question with intentional whitespace  "
	described, err := dataset.describe("valid")
	require.NoError(t, err)
	require.Equal(t, dataset.queries[1], described.QAPairs[0].Question)
	require.Equal(t, []int{10, 11}, described.QAPairs[0].PIDs)
}

func TestLoadDatasetRejectsDuplicateRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*[]TextInfo, *[]TextInfo, *[]TextInfo, *[]RelsInfo, *[]QaInfo)
		want   string
	}{
		{name: "query id", mutate: func(q, _, _ *[]TextInfo, _ *[]RelsInfo, _ *[]QaInfo) {
			*q = append(*q, TextInfo{ID: 1, Text: "duplicate"})
		}, want: "duplicate query id 1"},
		{name: "corpus id", mutate: func(_, c, _ *[]TextInfo, _ *[]RelsInfo, _ *[]QaInfo) {
			*c = append(*c, TextInfo{ID: 10, Text: "duplicate"})
		}, want: "duplicate corpus id 10"},
		{name: "answer id", mutate: func(_, _, a *[]TextInfo, _ *[]RelsInfo, _ *[]QaInfo) {
			*a = append(*a, TextInfo{ID: 20, Text: "duplicate"})
		}, want: "duplicate answer id 20"},
		{name: "qrel pair", mutate: func(_, _, _ *[]TextInfo, r *[]RelsInfo, _ *[]QaInfo) {
			*r = append(*r, RelsInfo{QID: 1, PID: 10})
		}, want: "duplicate qrel (qid=1, pid=10)"},
		{name: "qas qid", mutate: func(_, _, _ *[]TextInfo, _ *[]RelsInfo, qas *[]QaInfo) {
			*qas = append(*qas, QaInfo{QID: 1, AID: 20})
		}, want: "duplicate qas qid 1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queries := []TextInfo{{ID: 1, Text: "question"}}
			corpus := []TextInfo{{ID: 10, Text: "passage"}}
			answers := []TextInfo{{ID: 20, Text: "answer"}}
			qrels := []RelsInfo{{QID: 1, PID: 10}}
			qas := []QaInfo{{QID: 1, AID: 20}}
			tc.mutate(&queries, &corpus, &answers, &qrels, &qas)

			directory := t.TempDir()
			require.NoError(t, parquet.WriteFile(filepath.Join(directory, "queries.parquet"), queries))
			require.NoError(t, parquet.WriteFile(filepath.Join(directory, "corpus.parquet"), corpus))
			require.NoError(t, parquet.WriteFile(filepath.Join(directory, "answers.parquet"), answers))
			require.NoError(t, parquet.WriteFile(filepath.Join(directory, "qrels.parquet"), qrels))
			require.NoError(t, parquet.WriteFile(filepath.Join(directory, "qas.parquet"), qas))

			_, err := loadDataset(directory)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
