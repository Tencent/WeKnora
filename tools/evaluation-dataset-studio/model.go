package main

import (
	"strings"
	"time"
)

const projectSchemaVersion = 2

type DatasetProject struct {
	SchemaVersion       int        `json:"schema_version"`
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Description         string     `json:"description,omitempty"`
	Version             string     `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	NextPassageID       int64      `json:"next_passage_id"`
	NextQuestionID      int64      `json:"next_question_id"`
	GenerationProfileID string     `json:"generation_profile_id,omitempty"`
	EvaluationProfileID string     `json:"evaluation_profile_id,omitempty"`
	Passages            []Passage  `json:"passages"`
	Questions           []Question `json:"questions"`
}

type Passage struct {
	ID          int64             `json:"id"`
	Text        string            `json:"text"`
	Source      string            `json:"source,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ReviewState string            `json:"review_state,omitempty"`
}

type Question struct {
	ID                   int64             `json:"id"`
	Text                 string            `json:"text"`
	Answer               string            `json:"answer"`
	RelevantPassageIDs   []int64           `json:"relevant_passage_ids"`
	Category             string            `json:"category,omitempty"`
	Difficulty           string            `json:"difficulty,omitempty"`
	Tags                 []string          `json:"tags,omitempty"`
	ReviewState          string            `json:"review_state,omitempty"`
	AnswerKeyPoints      []string          `json:"answer_key_points,omitempty"`
	Answerable           *bool             `json:"answerable,omitempty"`
	ExpectedDocuments    []string          `json:"expected_documents,omitempty"`
	ForbiddenDocuments   []string          `json:"forbidden_documents,omitempty"`
	TestRole             string            `json:"test_role,omitempty"`
	RetrievalFilters     map[string]string `json:"retrieval_filters,omitempty"`
	DatasetVersion       string            `json:"dataset_version,omitempty"`
	AnnotationSource     string            `json:"annotation_source,omitempty"`
	SourceRunID          string            `json:"source_run_id,omitempty"`
	SourceDatasetVersion string            `json:"source_dataset_version,omitempty"`
	FailureReason        string            `json:"failure_reason,omitempty"`
}

type DatasetSummary struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	UpdatedAt     time.Time `json:"updated_at"`
	PassageCount  int       `json:"passage_count"`
	QuestionCount int       `json:"question_count"`
}

type createDatasetRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type TextRow struct {
	ID   int64  `parquet:"id"`
	Text string `parquet:"text"`
}

type QrelRow struct {
	QID int64 `parquet:"qid"`
	PID int64 `parquet:"pid"`
}

type QARow struct {
	QID int64 `parquet:"qid"`
	AID int64 `parquet:"aid"`
}

func newDatasetProject(req createDatasetRequest, now time.Time, settings ...WorkspaceSettings) *DatasetProject {
	project := &DatasetProject{
		SchemaVersion:  projectSchemaVersion,
		ID:             req.ID,
		Name:           req.Name,
		Description:    req.Description,
		Version:        req.Version,
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
		NextPassageID:  1,
		NextQuestionID: 1,
		Passages:       []Passage{},
		Questions:      []Question{},
	}
	if len(settings) > 0 {
		project.GenerationProfileID = settings[0].DefaultGenerationProfileID
		project.EvaluationProfileID = settings[0].DefaultEvaluationProfileID
	}
	return project
}

func summarizeProject(project *DatasetProject) DatasetSummary {
	return DatasetSummary{
		ID:            project.ID,
		Name:          project.Name,
		Version:       project.Version,
		UpdatedAt:     project.UpdatedAt,
		PassageCount:  len(project.Passages),
		QuestionCount: len(project.Questions),
	}
}

func normalizeProject(project *DatasetProject) {
	if project.SchemaVersion < projectSchemaVersion {
		project.SchemaVersion = projectSchemaVersion
	}
	project.GenerationProfileID = strings.TrimSpace(project.GenerationProfileID)
	project.EvaluationProfileID = strings.TrimSpace(project.EvaluationProfileID)
	if project.Passages == nil {
		project.Passages = []Passage{}
	}
	if project.Questions == nil {
		project.Questions = []Question{}
	}
	var maxPassageID int64
	for _, passage := range project.Passages {
		if passage.ID > maxPassageID {
			maxPassageID = passage.ID
		}
	}
	if project.NextPassageID <= maxPassageID {
		project.NextPassageID = maxPassageID + 1
	}
	if project.NextPassageID < 1 {
		project.NextPassageID = 1
	}
	var maxQuestionID int64
	for _, question := range project.Questions {
		if question.ID > maxQuestionID {
			maxQuestionID = question.ID
		}
	}
	if project.NextQuestionID <= maxQuestionID {
		project.NextQuestionID = maxQuestionID + 1
	}
	if project.NextQuestionID < 1 {
		project.NextQuestionID = 1
	}
}
