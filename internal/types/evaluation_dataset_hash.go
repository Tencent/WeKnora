package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Canonical hashing for evaluation dataset versions (schema version 1).
//
// Rules (target architecture section 6.1):
//   - passages sorted by PID, questions sorted by QID, relevance sorted by QID/PID;
//   - original UTF-8 content is preserved: no trim, no case folding, no Unicode normalization;
//   - JSON Lines with fixed field order and HTML escaping disabled, joined by "\n";
//   - the hash input covers the schema version and the record type of every line;
//   - question lines do not contain sample_index, so logically identical content
//     submitted in a different array order produces the same content hash.

const evaluationDatasetContentHashHeader = "weknora/evaluation-dataset-content@v1\n"

type evaluationDatasetPassageLine struct {
	Type     string          `json:"type"`
	PID      string          `json:"pid"`
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata"`
}

type evaluationDatasetQuestionLine struct {
	Type     string `json:"type"`
	QID      string `json:"qid"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type evaluationDatasetRelevanceLine struct {
	Type  string `json:"type"`
	QID   string `json:"qid"`
	PID   string `json:"pid"`
	Grade int    `json:"grade"`
}

// CanonicalEvaluationDatasetContentSHA256 computes the schema-version-1
// canonical content SHA-256 for one structured version input.
func CanonicalEvaluationDatasetContentSHA256(input *EvaluationDatasetVersionInput) string {
	var buffer bytes.Buffer
	buffer.WriteString(evaluationDatasetContentHashHeader)

	passages := append([]EvaluationDatasetPassageInput(nil), input.Passages...)
	sort.SliceStable(passages, func(i, j int) bool { return passages[i].PID < passages[j].PID })
	for _, passage := range passages {
		metadata := json.RawMessage(`{}`)
		if len(passage.Metadata) != 0 {
			metadata = json.RawMessage(append(json.RawMessage(nil), passage.Metadata...))
		}
		writeEvaluationDatasetHashLine(&buffer, evaluationDatasetPassageLine{
			Type:     "passage",
			PID:      passage.PID,
			Content:  passage.Content,
			Metadata: metadata,
		})
	}

	questions := append([]EvaluationDatasetQuestionInput(nil), input.Questions...)
	sort.SliceStable(questions, func(i, j int) bool { return questions[i].QID < questions[j].QID })
	for _, question := range questions {
		writeEvaluationDatasetHashLine(&buffer, evaluationDatasetQuestionLine{
			Type:     "question",
			QID:      question.QID,
			Question: question.Question,
			Answer:   question.Answer,
		})
	}

	relevance := append([]EvaluationDatasetRelevanceInput(nil), input.Relevance...)
	sort.SliceStable(relevance, func(i, j int) bool {
		if relevance[i].QID != relevance[j].QID {
			return relevance[i].QID < relevance[j].QID
		}
		return relevance[i].PID < relevance[j].PID
	})
	for _, edge := range relevance {
		writeEvaluationDatasetHashLine(&buffer, evaluationDatasetRelevanceLine{
			Type:  "relevance",
			QID:   edge.QID,
			PID:   edge.PID,
			Grade: edge.Grade,
		})
	}

	sum := sha256.Sum256(buffer.Bytes())
	return hex.EncodeToString(sum[:])
}

// EvaluationDatasetArtifactSHA256 hashes the imported artifact bytes (bundle
// file content or the canonical request payload) for tamper evidence.
func EvaluationDatasetArtifactSHA256(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func writeEvaluationDatasetHashLine(buffer *bytes.Buffer, line any) {
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	// json.Encoder.Encode appends exactly one '\n' per line.
	if err := encoder.Encode(line); err != nil {
		// Encoding plain structs cannot fail; keep the panic impossible to miss.
		panic("evaluation dataset canonical hash: " + err.Error())
	}
}
