package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
)

type manifest struct {
	Passages []passage `json:"passages"`
	Queries  []query   `json:"queries"`
}

type passage struct {
	PID  int64  `json:"pid"`
	Text string `json:"text"`
}

type query struct {
	QID          int64   `json:"qid"`
	Question     string  `json:"question"`
	AID          int64   `json:"aid"`
	Answer       string  `json:"answer"`
	RelevantPIDs []int64 `json:"relevant_pids"`
}

type textRow struct {
	ID   int64  `parquet:"id"`
	Text string `parquet:"text"`
}

type qrelRow struct {
	QID int64 `parquet:"qid"`
	PID int64 `parquet:"pid"`
}

type qaRow struct {
	QID int64 `parquet:"qid"`
	AID int64 `parquet:"aid"`
}

func main() {
	manifestPath := flag.String("manifest", "dataset/benchmark_v1/benchmark.json", "benchmark manifest path")
	outputDir := flag.String("output", "dataset/benchmark_v1", "Parquet output directory")
	flag.Parse()

	data, err := os.ReadFile(*manifestPath)
	check(err)

	var source manifest
	check(json.Unmarshal(data, &source))
	check(os.MkdirAll(*outputDir, 0o755))

	corpus := make([]textRow, 0, len(source.Passages))
	for _, item := range source.Passages {
		corpus = append(corpus, textRow{ID: item.PID, Text: item.Text})
	}

	queries := make([]textRow, 0, len(source.Queries))
	answers := make([]textRow, 0, len(source.Queries))
	qas := make([]qaRow, 0, len(source.Queries))
	qrels := make([]qrelRow, 0)
	for _, item := range source.Queries {
		queries = append(queries, textRow{ID: item.QID, Text: item.Question})
		answers = append(answers, textRow{ID: item.AID, Text: item.Answer})
		qas = append(qas, qaRow{QID: item.QID, AID: item.AID})
		for _, pid := range item.RelevantPIDs {
			qrels = append(qrels, qrelRow{QID: item.QID, PID: pid})
		}
	}

	write(*outputDir, "corpus.parquet", corpus)
	write(*outputDir, "queries.parquet", queries)
	write(*outputDir, "answers.parquet", answers)
	write(*outputDir, "qrels.parquet", qrels)
	write(*outputDir, "qas.parquet", qas)

	fmt.Printf("generated benchmark: corpus=%d queries=%d answers=%d qrels=%d qas=%d\n",
		len(corpus), len(queries), len(answers), len(qrels), len(qas))
}

func write[T any](directory, name string, rows []T) {
	check(parquet.WriteFile(filepath.Join(directory, name), rows))
}

func check(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
