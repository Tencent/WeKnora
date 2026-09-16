package service

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"

	builtindataset "github.com/Tencent/WeKnora/dataset"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/parquet-go/parquet-go"
)

const builtinEvaluationDatasetArtifactSHA256 = "d598d48018f1da0705920f4432b68a9309c391adbe1403bb1278771b22fe923f"

var builtinEvaluationDatasetFiles = []string{
	"answers.parquet",
	"corpus.parquet",
	"qas.parquet",
	"qrels.parquet",
	"queries.parquet",
}

// BuiltinEvaluationDatasetRegistration converts the embedded Parquet bundle
// into the structured registry input and pins the raw artifact bytes.
func BuiltinEvaluationDatasetRegistration() (*types.EvaluationBuiltinDatasetRegistration, error) {
	files := make(map[string][]byte, len(builtinEvaluationDatasetFiles))
	var artifact bytes.Buffer
	for _, name := range builtinEvaluationDatasetFiles {
		data, err := builtindataset.Samples.ReadFile("samples/" + name)
		if err != nil {
			return nil, fmt.Errorf("load built-in evaluation dataset %s: %w", name, err)
		}
		files[name] = data
		artifact.WriteString(name)
		artifact.WriteByte(0)
		artifact.Write(data)
		artifact.WriteByte(0)
	}
	actual := types.EvaluationDatasetArtifactSHA256(artifact.Bytes())
	if actual != builtinEvaluationDatasetArtifactSHA256 {
		return nil, fmt.Errorf("load built-in evaluation dataset: artifact %s != pinned %s",
			actual, builtinEvaluationDatasetArtifactSHA256)
	}

	queries, err := readEvaluationParquet[TextInfo](files["queries.parquet"])
	if err != nil {
		return nil, fmt.Errorf("load built-in evaluation queries: %w", err)
	}
	corpus, err := readEvaluationParquet[TextInfo](files["corpus.parquet"])
	if err != nil {
		return nil, fmt.Errorf("load built-in evaluation corpus: %w", err)
	}
	answers, err := readEvaluationParquet[TextInfo](files["answers.parquet"])
	if err != nil {
		return nil, fmt.Errorf("load built-in evaluation answers: %w", err)
	}
	qrels, err := readEvaluationParquet[RelsInfo](files["qrels.parquet"])
	if err != nil {
		return nil, fmt.Errorf("load built-in evaluation relevance: %w", err)
	}
	qas, err := readEvaluationParquet[QaInfo](files["qas.parquet"])
	if err != nil {
		return nil, fmt.Errorf("load built-in evaluation question answers: %w", err)
	}

	content, err := evaluationParquetContent(queries, corpus, answers, qrels, qas)
	if err != nil {
		return nil, err
	}
	return &types.EvaluationBuiltinDatasetRegistration{
		DatasetID:              "default",
		Name:                   "Built-in evaluation samples",
		Description:            "Version-controlled Parquet evaluation samples",
		Content:                content,
		ArtifactBytes:          artifact.Bytes(),
		ExpectedArtifactSHA256: builtinEvaluationDatasetArtifactSHA256,
		Manifest:               types.JSON(`{"source":"embedded_parquet","schema_version":1}`),
	}, nil
}

func readEvaluationParquet[T any](data []byte) ([]T, error) {
	return parquet.Read[T](bytes.NewReader(data), int64(len(data)))
}

func evaluationParquetContent(
	queries []TextInfo,
	corpus []TextInfo,
	answers []TextInfo,
	qrels []RelsInfo,
	qas []QaInfo,
) (*types.EvaluationDatasetVersionInput, error) {
	answerByID := make(map[int64]string, len(answers))
	for _, answer := range answers {
		if _, duplicate := answerByID[answer.ID]; duplicate {
			return nil, fmt.Errorf("load built-in evaluation dataset: duplicate answer id %d", answer.ID)
		}
		answerByID[answer.ID] = answer.Text
	}
	answerIDByQID := make(map[int64]int64, len(qas))
	for _, relation := range qas {
		if _, duplicate := answerIDByQID[relation.QID]; duplicate {
			return nil, fmt.Errorf("load built-in evaluation dataset: duplicate answer for qid %d", relation.QID)
		}
		if _, ok := answerByID[relation.AID]; !ok {
			return nil, fmt.Errorf("load built-in evaluation dataset: qid %d references unknown answer %d",
				relation.QID, relation.AID)
		}
		answerIDByQID[relation.QID] = relation.AID
	}

	content := &types.EvaluationDatasetVersionInput{}
	pidSet := make(map[int64]struct{}, len(corpus))
	sort.Slice(corpus, func(i, j int) bool { return corpus[i].ID < corpus[j].ID })
	for _, passage := range corpus {
		if _, duplicate := pidSet[passage.ID]; duplicate {
			return nil, fmt.Errorf("load built-in evaluation dataset: duplicate pid %d", passage.ID)
		}
		pidSet[passage.ID] = struct{}{}
		content.Passages = append(content.Passages, types.EvaluationDatasetPassageInput{
			PID: strconv.FormatInt(passage.ID, 10), Content: passage.Text,
		})
	}

	qidSet := make(map[int64]struct{}, len(queries))
	sort.Slice(queries, func(i, j int) bool { return queries[i].ID < queries[j].ID })
	for _, query := range queries {
		if _, duplicate := qidSet[query.ID]; duplicate {
			return nil, fmt.Errorf("load built-in evaluation dataset: duplicate qid %d", query.ID)
		}
		qidSet[query.ID] = struct{}{}
		answer := ""
		if answerID, ok := answerIDByQID[query.ID]; ok {
			answer = answerByID[answerID]
		}
		content.Questions = append(content.Questions, types.EvaluationDatasetQuestionInput{
			QID: strconv.FormatInt(query.ID, 10), Question: query.Text, Answer: answer,
		})
	}

	sort.Slice(qrels, func(i, j int) bool {
		if qrels[i].QID != qrels[j].QID {
			return qrels[i].QID < qrels[j].QID
		}
		return qrels[i].PID < qrels[j].PID
	})
	var previous *RelsInfo
	for index := range qrels {
		relation := qrels[index]
		if _, ok := qidSet[relation.QID]; !ok {
			return nil, fmt.Errorf("load built-in evaluation dataset: relevance references unknown qid %d",
				relation.QID)
		}
		if _, ok := pidSet[relation.PID]; !ok {
			return nil, fmt.Errorf("load built-in evaluation dataset: relevance references unknown pid %d",
				relation.PID)
		}
		if previous != nil && previous.QID == relation.QID && previous.PID == relation.PID {
			return nil, errors.New("load built-in evaluation dataset: duplicate relevance edge")
		}
		content.Relevance = append(content.Relevance, types.EvaluationDatasetRelevanceInput{
			QID:   strconv.FormatInt(relation.QID, 10),
			PID:   strconv.FormatInt(relation.PID, 10),
			Grade: 1,
		})
		previous = &qrels[index]
	}
	return content, nil
}
