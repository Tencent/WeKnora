package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestWorkspaceDefaultsBindNewDatasetAndProtectReferences(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveGenerationConnection("shared-generator", &GenerationConnection{
		ID: "shared-generator", Name: "共享生成模型", BaseURL: "https://example.com/v1", APIKey: "test-secret", Model: "test-model",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveConnectionProfile("shared-environment", &ConnectionProfile{
		ID: "shared-environment", Name: "共享手工环境", Environment: "production", AdapterID: manualExportAdapterID, DatasetID: "default",
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := store.saveWorkspaceSettings(WorkspaceSettings{
		DefaultGenerationProfileID: "shared-generator",
		DefaultEvaluationProfileID: "shared-environment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.SchemaVersion != workspaceSettingsSchemaVersion {
		t.Fatalf("settings schema = %d", settings.SchemaVersion)
	}
	project, err := store.create(createDatasetRequest{ID: "uses-defaults", Name: "默认配置测试", Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if project.SchemaVersion != projectSchemaVersion || project.GenerationProfileID != "shared-generator" || project.EvaluationProfileID != "shared-environment" {
		t.Fatalf("unexpected project bindings: %#v", project)
	}
	refs, err := store.configurationReferences("generation", "shared-generator")
	if err != nil {
		t.Fatal(err)
	}
	if !refs.WorkspaceDefault || len(refs.DatasetIDs) != 1 || refs.DatasetIDs[0] != project.ID {
		t.Fatalf("unexpected references: %#v", refs)
	}
	if err := store.deleteGenerationConnection("shared-generator"); err == nil || !strings.Contains(err.Error(), "仍被引用") {
		t.Fatalf("delete referenced generation profile error = %v", err)
	}
	if err := store.deleteConnectionProfile("shared-environment"); err == nil || !strings.Contains(err.Error(), "仍被引用") {
		t.Fatalf("delete referenced evaluation profile error = %v", err)
	}
}

func TestLegacyProjectNormalizesToSchemaV2(t *testing.T) {
	project := &DatasetProject{SchemaVersion: 1, ID: "legacy", Name: "旧项目", Version: "0.1.0"}
	normalizeProject(project)
	if project.SchemaVersion != projectSchemaVersion {
		t.Fatalf("schema version = %d, want %d", project.SchemaVersion, projectSchemaVersion)
	}
}

func TestSavedAPIKeysSurviveBlankProfileUpdates(t *testing.T) {
	root := t.TempDir()
	store, err := newProjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	generation := &GenerationConnection{ID: "saved-generator", Name: "生成配置", BaseURL: "https://example.com/v1", APIKey: "generation-secret", Model: "model-a"}
	if _, err := store.saveGenerationConnection(generation.ID, generation); err != nil {
		t.Fatal(err)
	}
	generation.APIKey = ""
	generation.Name = "生成配置已更新"
	savedGeneration, err := store.saveGenerationConnection(generation.ID, generation)
	if err != nil {
		t.Fatal(err)
	}
	if savedGeneration.APIKey != "generation-secret" {
		t.Fatal("blank generation update cleared the saved API key")
	}

	connection := &ConnectionProfile{
		ID: "saved-environment", Name: "本地环境", Environment: "local", AdapterID: "local-current",
		BaseURL: "http://127.0.0.1:8080", APIKey: "environment-secret", DatasetID: "default",
	}
	if _, err := store.saveConnectionProfile(connection.ID, connection); err != nil {
		t.Fatal(err)
	}
	connection.APIKey = ""
	connection.Name = "本地环境已更新"
	savedConnection, err := store.saveConnectionProfile(connection.ID, connection)
	if err != nil {
		t.Fatal(err)
	}
	if savedConnection.APIKey != "environment-secret" {
		t.Fatal("blank environment update cleared the saved API key")
	}
	for _, path := range []string{
		filepath.Join(root, "generation-connections", "saved-generator.json"),
		filepath.Join(root, "connections", "saved-environment.json"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func validProject() *DatasetProject {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	return &DatasetProject{
		SchemaVersion:  projectSchemaVersion,
		ID:             "customer-service-eval",
		Name:           "客服知识库评测集",
		Version:        "0.1.0",
		CreatedAt:      now,
		UpdatedAt:      now,
		NextPassageID:  21,
		NextQuestionID: 3,
		Passages: []Passage{
			{ID: 20, Text: "设备离线时，先检查网关电源和网络连接。", Source: "故障手册"},
			{ID: 3, Text: "长按重置键十秒，指示灯闪烁后设备恢复出厂设置。", Source: "产品手册"},
		},
		Questions: []Question{
			{ID: 2, Text: "设备离线怎么排查？", Answer: "先检查网关电源和网络连接。", RelevantPassageIDs: []int64{20}},
			{ID: 1, Text: "如何恢复出厂设置？", Answer: "长按重置键十秒。", RelevantPassageIDs: []int64{3}},
		},
	}
}

func TestValidateProject(t *testing.T) {
	project := validProject()
	report := validateProject(project)
	if !report.Valid || report.ErrorCount != 0 {
		t.Fatalf("valid project report = %#v", report)
	}
	if report.Statistics == nil || report.Statistics.PassageCoveragePercent != 100 || report.Statistics.QrelCount != 2 {
		t.Fatalf("statistics = %#v", report.Statistics)
	}

	project.Questions[0].Answer = ""
	project.Questions[1].RelevantPassageIDs = []int64{999}
	report = validateProject(project)
	if report.Valid || report.ErrorCount < 2 {
		t.Fatalf("invalid project report = %#v", report)
	}
}

func TestImportProjectCSV(t *testing.T) {
	project := newDatasetProject(createDatasetRequest{ID: "csv-eval", Name: "CSV 测试", Version: "0.1.0"}, time.Now())
	passageCSV := "\ufeffid,text,source,tags,review_state\n10,设备离线时先检查网关电源和网络连接,故障手册,离线|网络,approved\n,恢复出厂设置前请备份设备配置,产品手册,重置,draft\n"
	summary, err := importProjectCSV(project, csvImportRequest{Kind: "passages", CSV: passageCSV})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ImportedCount != 2 || len(project.Passages) != 2 || project.Passages[1].ID != 11 || project.NextPassageID != 12 {
		t.Fatalf("summary = %#v, project = %#v", summary, project)
	}
	if !reflect.DeepEqual(project.Passages[0].Tags, []string{"离线", "网络"}) {
		t.Fatalf("tags = %#v", project.Passages[0].Tags)
	}

	questionCSV := "text,answer,relevant_passage_ids,category,difficulty,tags,review_state,answer_key_points,answerable,expected_documents,forbidden_documents,test_role,retrieval_filters,dataset_version,annotation_source\n设备离线怎么排查？,检查网关电源和网络,10|11,故障排查,easy,离线|网络,approved,检查电源|检查网络,false,故障手册|网络指南,旧版手册,售后人员,department=售后|product=网关,0.1.0,业务专家\n"
	summary, err = importProjectCSV(project, csvImportRequest{Kind: "questions", CSV: questionCSV})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ImportedCount != 1 || len(project.Questions) != 1 || !reflect.DeepEqual(project.Questions[0].RelevantPassageIDs, []int64{10, 11}) {
		t.Fatalf("summary = %#v, questions = %#v", summary, project.Questions)
	}
	question := project.Questions[0]
	if question.Answerable == nil || *question.Answerable || !reflect.DeepEqual(question.AnswerKeyPoints, []string{"检查电源", "检查网络"}) ||
		!reflect.DeepEqual(question.ExpectedDocuments, []string{"故障手册", "网络指南"}) || question.ForbiddenDocuments[0] != "旧版手册" ||
		question.TestRole != "售后人员" || question.RetrievalFilters["department"] != "售后" || question.DatasetVersion != "0.1.0" || question.AnnotationSource != "业务专家" {
		t.Fatalf("question metadata = %#v", question)
	}
}

func TestImportQuestionCSVRejectsInvalidEvaluationMetadataAtomically(t *testing.T) {
	for _, csvText := range []string{
		"text,answer,relevant_passage_ids,answerable\n新问题,新答案,3,可能\n",
		"text,answer,relevant_passage_ids,retrieval_filters\n新问题,新答案,3,department\n",
		"text,answer,relevant_passage_ids,retrieval_filters\n新问题,新答案,3,department=售后|department=研发\n",
	} {
		project := validProject()
		before := len(project.Questions)
		if _, err := importProjectCSV(project, csvImportRequest{Kind: "questions", CSV: csvText}); err == nil {
			t.Fatalf("expected invalid metadata error for %q", csvText)
		}
		if len(project.Questions) != before {
			t.Fatalf("questions changed after failed import: %d -> %d", before, len(project.Questions))
		}
	}
}

func TestImportProjectCSVRejectsMissingPassageAtomically(t *testing.T) {
	project := validProject()
	before := len(project.Questions)
	_, err := importProjectCSV(project, csvImportRequest{
		Kind: "questions",
		CSV:  "text,answer,relevant_passage_ids\n新问题,新答案,999\n",
	})
	if err == nil {
		t.Fatal("expected missing passage error")
	}
	if len(project.Questions) != before {
		t.Fatalf("questions changed after failed import: %d -> %d", before, len(project.Questions))
	}
}

func TestImportProjectCSVRejectsDuplicateRelations(t *testing.T) {
	project := validProject()
	_, err := importProjectCSV(project, csvImportRequest{
		Kind: "questions",
		CSV:  "text,answer,relevant_passage_ids\n新问题,新答案,3|3\n",
	})
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("error = %v", err)
	}
}

func TestProjectFixtures(t *testing.T) {
	for _, test := range []struct {
		name      string
		wantValid bool
	}{
		{name: "valid-project.json", wantValid: true},
		{name: "invalid-project.json", wantValid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", test.name))
			if err != nil {
				t.Fatal(err)
			}
			var project DatasetProject
			if err := json.Unmarshal(data, &project); err != nil {
				t.Fatal(err)
			}
			if got := validateProject(&project).Valid; got != test.wantValid {
				t.Fatalf("valid = %v, want %v", got, test.wantValid)
			}
		})
	}
}

func TestBuildExportRowsRemapsPassagesDeterministically(t *testing.T) {
	rows, err := buildExportRows(validProject())
	if err != nil {
		t.Fatal(err)
	}
	wantCorpus := []TextRow{
		{ID: 0, Text: "长按重置键十秒，指示灯闪烁后设备恢复出厂设置。"},
		{ID: 1, Text: "设备离线时，先检查网关电源和网络连接。"},
	}
	if !reflect.DeepEqual(rows.Corpus, wantCorpus) {
		t.Fatalf("corpus = %#v, want %#v", rows.Corpus, wantCorpus)
	}
	wantQrels := []QrelRow{{QID: 1, PID: 0}, {QID: 2, PID: 1}}
	if !reflect.DeepEqual(rows.Qrels, wantQrels) {
		t.Fatalf("qrels = %#v, want %#v", rows.Qrels, wantQrels)
	}
	if got := rows.QAs; !reflect.DeepEqual(got, []QARow{{QID: 1, AID: 1}, {QID: 2, AID: 2}}) {
		t.Fatalf("qas = %#v", got)
	}
}

func TestEvaluationMetadataDoesNotChangeWeKnoraExportRows(t *testing.T) {
	baseline, err := buildExportRows(validProject())
	if err != nil {
		t.Fatal(err)
	}
	project := validProject()
	answerable := false
	project.Questions[0].AnswerKeyPoints = []string{"检查电源", "检查网络"}
	project.Questions[0].Answerable = &answerable
	project.Questions[0].ExpectedDocuments = []string{"故障手册"}
	project.Questions[0].ForbiddenDocuments = []string{"旧版手册"}
	project.Questions[0].TestRole = "售后人员"
	project.Questions[0].RetrievalFilters = map[string]string{"department": "售后"}
	project.Questions[0].DatasetVersion = "0.1.0"
	project.Questions[0].AnnotationSource = "业务专家"
	withMetadata, err := buildExportRows(project)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withMetadata, baseline) {
		t.Fatalf("evaluation metadata changed strict export rows:\nwith metadata = %#v\nbaseline = %#v", withMetadata, baseline)
	}
}

func TestStoreRoundTripAndTrashDelete(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) }
	project, err := store.create(createDatasetRequest{ID: "test-eval", Name: "测试集", Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	project.Passages = validProject().Passages
	project.Questions = validProject().Questions
	answerable := true
	project.Questions[0].Answerable = &answerable
	project.Questions[0].AnswerKeyPoints = []string{"检查网关"}
	project.Questions[0].RetrievalFilters = map[string]string{"department": "售后"}
	if err := store.save(project.ID, project); err != nil {
		t.Fatal(err)
	}
	got, err := store.get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "测试集" || len(got.Questions) != 2 || got.Questions[0].Answerable == nil || !*got.Questions[0].Answerable || got.Questions[0].RetrievalFilters["department"] != "售后" {
		t.Fatalf("project = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(store.backups, "test-eval-latest.json")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if err := store.delete(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.get(project.ID); err != errDatasetNotFound {
		t.Fatalf("get after delete error = %v", err)
	}
	entries, err := os.ReadDir(store.trash)
	if err != nil || len(entries) != 1 {
		t.Fatalf("trash entries = %v, err = %v", entries, err)
	}
}

func TestExportWeKnoraZipContract(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, summary, err := store.exportWeKnora(validProject())
	if err != nil {
		t.Fatal(err)
	}
	if summary.QuestionCount != 2 || summary.PassageCount != 2 || summary.QrelCount != 2 {
		t.Fatalf("summary = %#v", summary)
	}

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var names []string
	extractDir := t.TempDir()
	for _, file := range reader.File {
		names = append(names, file.Name)
		source, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.Create(filepath.Join(extractDir, file.Name))
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(target, source)
		source.Close()
		target.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	sort.Strings(names)
	wantNames := append([]string(nil), exportFileNames...)
	sort.Strings(wantNames)
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("zip names = %v, want %v", names, wantNames)
	}
	queries, err := parquet.ReadFile[TextRow](filepath.Join(extractDir, "queries.parquet"))
	if err != nil || len(queries) != 2 {
		t.Fatalf("queries rows = %v, err = %v", queries, err)
	}
	corpus, err := parquet.ReadFile[TextRow](filepath.Join(extractDir, "corpus.parquet"))
	if err != nil || len(corpus) != 2 || corpus[0].ID != 0 || corpus[1].ID != 1 {
		t.Fatalf("corpus rows = %v, err = %v", corpus, err)
	}
}

func TestWeKnoraZipRoundTripImport(t *testing.T) {
	sourceStore, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := sourceStore.exportWeKnora(validProject())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	targetStore, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, summary, err := targetStore.importWeKnora(weKnoraImportRequest{
		ID:        "imported-eval",
		Name:      "导入评测集",
		Version:   "1.0.0",
		ZIPBase64: base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.QuestionCount != 2 || summary.PassageCount != 2 || summary.QrelCount != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if project.ID != "imported-eval" || len(project.Questions) != 2 || len(project.Passages) != 2 {
		t.Fatalf("project = %#v", project)
	}
	if got := project.Questions[0].RelevantPassageIDs; !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("first question relations = %#v", got)
	}
	if report := validateProject(project); !report.Valid {
		t.Fatalf("imported project report = %#v", report)
	}
}

func TestWeKnoraImportRejectsMultipleAnswersPerQuestion(t *testing.T) {
	rows := exportRows{
		Queries: []TextRow{{ID: 1, Text: "问题文本"}},
		Corpus:  []TextRow{{ID: 0, Text: "这是一段长度足够用于测试的语料文本内容。"}},
		Answers: []TextRow{{ID: 1, Text: "答案一"}, {ID: 2, Text: "答案二"}},
		Qrels:   []QrelRow{{QID: 1, PID: 0}},
		QAs:     []QARow{{QID: 1, AID: 1}, {QID: 1, AID: 2}},
	}
	_, err := projectFromWeKnoraRows(weKnoraImportRequest{ID: "invalid", Name: "无效", Version: "1"}, rows, time.Now())
	if err == nil || !strings.Contains(err.Error(), "一问一答") {
		t.Fatalf("error = %v", err)
	}
}

func TestSnapshotAndExportHistory(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 18, 9, 30, 0, 123456789, time.UTC)
	store.now = func() time.Time { return fixed }
	project := validProject()
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.createSnapshot(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != historyID(fixed) || snapshot.QuestionCount != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	project.Name = "已修改名称"
	project.Questions = project.Questions[:1]
	if err := store.save(project.ID, project); err != nil {
		t.Fatal(err)
	}
	restored, err := store.restoreSnapshot(project.ID, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != "客服知识库评测集" || len(restored.Questions) != 2 {
		t.Fatalf("restored = %#v", restored)
	}

	path, exported, err := store.exportWeKnora(restored)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.listExports(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != exported.ID || items[0].Size <= 0 {
		t.Fatalf("exports = %#v", items)
	}
	wantSHA256, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if exported.ContractProfileID != weKnoraFiveParquetProfileID || exported.SHA256 != wantSHA256 || len(exported.SHA256) != 64 || exported.DeploymentStatus != exportDeploymentNotDeployed {
		t.Fatalf("export traceability = %#v", exported)
	}
	historyPath, historyItem, err := store.exportHistoryPath(project.ID, exported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if historyPath != path || historyItem.FileName != exported.FileName {
		t.Fatalf("history path = %q, item = %#v", historyPath, historyItem)
	}
	profile, err := store.saveConnectionProfile("local-dev", &ConnectionProfile{
		ID: "local-dev", Name: "本地开发", Environment: "local",
		BaseURL: "http://127.0.0.1:8080", AdapterID: localCurrentAdapterID, DatasetID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployed, err := store.updateExportDeployment(project.ID, exported.ID, exportDeploymentRequest{
		ConnectionProfileID: profile.ID, Status: exportDeploymentDeployed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployed.DeploymentStatus != exportDeploymentDeployed || deployed.AdapterID != localCurrentAdapterID || deployed.DatasetID != "default" || deployed.DeployedAt == nil {
		t.Fatalf("deployed export = %#v", deployed)
	}
	if _, err := store.updateExportDeployment(project.ID, exported.ID, exportDeploymentRequest{
		ConnectionProfileID: profile.ID, DatasetID: "production-v2", Status: exportDeploymentDeployed,
	}); err == nil {
		t.Fatal("local-current should reject a non-default dataset ID")
	}
}

func TestEvaluationRunStartPollAndSecretHandling(t *testing.T) {
	const secret = "sk-evaluation-secret"
	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-API-Key") != secret {
			t.Errorf("api key header = %q", r.Header.Get("X-API-Key"))
		}
		body := ""
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/evaluation":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["dataset_id"] != "default" || request["knowledge_base_id"] != "kb-1" {
				t.Errorf("request = %#v", request)
			}
			body = `{"success":true,"data":{"task":{"id":"task-1","dataset_id":"default","status":1}}}`
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/evaluation":
			if r.URL.Query().Get("task_id") != "task-1" {
				t.Errorf("task_id = %q", r.URL.Query().Get("task_id"))
			}
			body = `{"success":true,"data":{"task":{"id":"task-1","dataset_id":"default","status":2,"total":2,"finished":2},"metric":{"retrieval_metrics":{"precision":0.75,"recall":1},"generation_metrics":{"rouge1":0.5}}}}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"success":false}`)), Header: http.Header{}, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := validProject()
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	project, err = store.get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, exported, err := store.exportWeKnora(project)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("evaluation-local", &ConnectionProfile{
		ID: "evaluation-local", Name: "评测本地环境", Environment: "local",
		BaseURL: "http://127.0.0.1:8080", AdapterID: localCurrentAdapterID, DatasetID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.startEvaluation(project.ID, evaluationStartRequest{
		ConnectionProfileID: profile.ID,
		BaseURL:             "http://weknora.test",
		APIKey:              secret,
		KnowledgeBaseID:     "kb-1",
		ChatModelID:         "chat-1",
		RerankModelID:       "rerank-1",
		ExportID:            exported.ID,
		DatasetDeployed:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.TaskID != "task-1" || run.Status != 1 {
		t.Fatalf("run = %#v", run)
	}
	if run.AdapterID != localCurrentAdapterID || run.ConnectionProfileID != profile.ID || run.Environment != "local" || run.ContractProfileID != weKnoraFiveParquetProfileID || len(run.ExportSHA256) != 64 || run.DatasetID != "default" {
		t.Fatalf("run traceability = %#v", run)
	}
	recordData, err := os.ReadFile(filepath.Join(store.evaluations, project.ID, run.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(recordData, []byte(secret)) {
		t.Fatal("API key was persisted in evaluation record")
	}

	run, err = store.pollEvaluation(project.ID, run.ID, evaluationPollRequest{APIKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != 2 || run.Finished != 2 || run.Metric == nil || run.Metric.RetrievalMetrics["precision"] != 0.75 {
		t.Fatalf("polled run = %#v", run)
	}
	runs, err := store.listEvaluations(project.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != 2 {
		t.Fatalf("runs = %#v, err = %v", runs, err)
	}
}

func TestNormalizeEvaluationBaseURL(t *testing.T) {
	for input, want := range map[string]string{
		"http://localhost:8080":       "http://localhost:8080/api/v1",
		"https://example.com/api/v1/": "https://example.com/api/v1",
	} {
		got, err := normalizeEvaluationBaseURL(input)
		if err != nil || got != want {
			t.Fatalf("normalize %q = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeEvaluationBaseURL("https://example.com/custom"); err == nil {
		t.Fatal("expected invalid path error")
	}
}

func TestLoadEvaluationResources(t *testing.T) {
	const secret = "sk-resource-secret"
	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("X-API-Key") != secret {
			t.Errorf("request = %s %s, api key = %q", r.Method, r.URL.Path, r.Header.Get("X-API-Key"))
		}
		var body string
		switch r.URL.Path {
		case "/api/v1/knowledge-bases":
			body = `{"success":true,"data":[{"id":"kb-2","name":"Beta"},{"id":"kb-1","name":"Alpha"}]}`
		case "/api/v1/models":
			body = `{"success":true,"data":[{"id":"chat-2","name":"raw-chat","display_name":"Chat Beta","type":"KnowledgeQA","status":"active"},{"id":"chat-1","name":"Chat Alpha","type":"KnowledgeQA","status":"active"},{"id":"rerank-1","name":"Rerank","type":"Rerank","status":"active"},{"id":"embedding-1","name":"Embedding","type":"Embedding","status":"active"},{"id":"chat-downloading","name":"Unavailable","type":"KnowledgeQA","status":"downloading"}]}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"success":false}`)), Header: http.Header{}, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	resources, err := loadEvaluationResources(evaluationResourcesRequest{
		BaseURL: "http://weknora.test", APIKey: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resources.BaseURL != "http://weknora.test/api/v1" {
		t.Fatalf("base URL = %q", resources.BaseURL)
	}
	if resources.AdapterID != localCurrentAdapterID || !resources.Capabilities.StartEvaluation {
		t.Fatalf("adapter metadata = %#v", resources)
	}
	if got := []string{resources.KnowledgeBases[0].ID, resources.KnowledgeBases[1].ID}; !reflect.DeepEqual(got, []string{"kb-1", "kb-2"}) {
		t.Fatalf("knowledge bases = %#v", resources.KnowledgeBases)
	}
	if got := []string{resources.ChatModels[0].ID, resources.ChatModels[1].ID}; !reflect.DeepEqual(got, []string{"chat-1", "chat-2"}) {
		t.Fatalf("chat models = %#v", resources.ChatModels)
	}
	if len(resources.RerankModels) != 1 || resources.RerankModels[0].ID != "rerank-1" {
		t.Fatalf("rerank models = %#v", resources.RerankModels)
	}
}

func TestLocalCurrentRemoteErrorUsesNestedMessageAndRedactsSensitiveValues(t *testing.T) {
	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"success":false,"error":{"code":1007,"message":"task not found at https://internal.example/evaluation using sk-production-secret"}}`
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	_, err := localCurrentAdapter{}.PollEvaluation("https://public.example/api/v1", "test-key", "task-1")
	if err == nil {
		t.Fatal("expected remote evaluation error")
	}
	message := err.Error()
	if !strings.Contains(message, "HTTP 500") || !strings.Contains(message, "task not found") {
		t.Fatalf("nested error message was not preserved: %q", message)
	}
	if strings.Contains(message, "internal.example") || strings.Contains(message, "sk-production-secret") {
		t.Fatalf("remote error leaked sensitive values: %q", message)
	}
}

func TestRemoteAdapterRegistryAndManualExport(t *testing.T) {
	adapter, err := resolveRemoteAdapter("")
	if err != nil || adapter.ID() != localCurrentAdapterID {
		t.Fatalf("default adapter = %#v, err = %v", adapter, err)
	}
	manual, err := resolveRemoteAdapter(manualExportAdapterID)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities := manual.Capabilities(); capabilities.StartEvaluation || capabilities.PollEvaluation || capabilities.DiscoverModels || capabilities.ImportKnowledgeChunks {
		t.Fatalf("manual capabilities = %#v", capabilities)
	}
	if _, ok := manual.(ChunkSourceAdapter); ok {
		t.Fatal("manual-export should not implement the chunk source contract")
	}
	if _, err := manual.DiscoverResources("", ""); err == nil {
		t.Fatal("manual-export should reject resource discovery")
	}
	if _, err := resolveRemoteAdapter("unknown-version"); err == nil {
		t.Fatal("unknown adapter should be rejected")
	}
}

func TestLocalCurrentContractFixtures(t *testing.T) {
	readFixture := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "contracts", "local-current", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	var knowledgeBases localCurrentListEnvelope
	if err := json.Unmarshal(readFixture("knowledge-bases.json"), &knowledgeBases); err != nil {
		t.Fatal(err)
	}
	var knowledgeBaseItems []localCurrentKnowledgeBase
	if err := json.Unmarshal(knowledgeBases.Data, &knowledgeBaseItems); err != nil || len(knowledgeBaseItems) != 1 {
		t.Fatalf("knowledge base fixture = %#v, err = %v", knowledgeBaseItems, err)
	}

	var models localCurrentListEnvelope
	if err := json.Unmarshal(readFixture("models.json"), &models); err != nil {
		t.Fatal(err)
	}
	var modelItems []localCurrentModel
	if err := json.Unmarshal(models.Data, &modelItems); err != nil || len(modelItems) != 3 {
		t.Fatalf("model fixture = %#v, err = %v", modelItems, err)
	}

	var knowledge localCurrentSourceEnvelope[SourceKnowledge]
	if err := json.Unmarshal(readFixture("knowledge.json"), &knowledge); err != nil || len(knowledge.Data) != 1 {
		t.Fatalf("knowledge fixture = %#v, err = %v", knowledge, err)
	}
	if err := validateSourceKnowledge(knowledge.Data); err != nil {
		t.Fatalf("knowledge fixture validation: %v", err)
	}

	var chunks localCurrentSourceEnvelope[SourceChunk]
	if err := json.Unmarshal(readFixture("chunks.json"), &chunks); err != nil || len(chunks.Data) != 1 {
		t.Fatalf("chunk fixture = %#v, err = %v", chunks, err)
	}
	if err := validateSourceChunks(chunks.Data); err != nil {
		t.Fatalf("chunk fixture validation: %v", err)
	}

	for _, name := range []string{"evaluation-start.json", "evaluation-complete.json"} {
		var envelope localCurrentEvaluationEnvelope
		if err := json.Unmarshal(readFixture(name), &envelope); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !envelope.Success || envelope.Data.Task.ID == "" || !envelope.Data.Task.Status.valid() {
			t.Fatalf("%s: invalid envelope %#v", name, envelope)
		}
	}
}

func TestLocalCurrentChunkSourcePaginationContract(t *testing.T) {
	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("X-API-Key") != "saved-key" {
			t.Fatalf("source request = %s key=%q", r.Method, r.Header.Get("X-API-Key"))
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "3" {
			t.Fatalf("source pagination = %q", r.URL.RawQuery)
		}
		body := `{"success":true,"data":[],"total":0,"page":2,"page_size":3}`
		switch r.URL.Path {
		case "/api/v1/knowledge-bases":
			body = `{"success":true,"data":[{"id":"kb-1","name":"知识库"}],"total":1,"page":2,"page_size":3}`
		case "/api/v1/knowledge-bases/kb-1/knowledge":
			body = `{"success":true,"data":[{"id":"doc-1","knowledge_base_id":"kb-1","title":"文档"}],"total":1,"page":2,"page_size":3}`
		case "/api/v1/chunks/doc-1":
			body = `{"success":true,"data":[{"id":"chunk-1","knowledge_id":"doc-1","content":"正文"}],"total":1,"page":2,"page_size":3}`
		default:
			t.Fatalf("unexpected source path %q", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	adapter := localCurrentAdapter{}
	page := SourcePageRequest{Page: 2, PageSize: 3}
	if result, err := adapter.ListSourceKnowledgeBases("https://example.com", "saved-key", page); err != nil || len(result.Items) != 1 || result.Items[0].ID != "kb-1" {
		t.Fatalf("knowledge bases = %#v, err = %v", result, err)
	}
	if result, err := adapter.ListSourceKnowledge("https://example.com", "saved-key", "kb-1", page); err != nil || len(result.Items) != 1 || result.Items[0].ID != "doc-1" {
		t.Fatalf("knowledge = %#v, err = %v", result, err)
	}
	if result, err := adapter.ListSourceChunks("https://example.com", "saved-key", "doc-1", page); err != nil || len(result.Items) != 1 || result.Items[0].ID != "chunk-1" {
		t.Fatalf("chunks = %#v, err = %v", result, err)
	}
}

func TestLocalCurrentChunkSourceErrorClassification(t *testing.T) {
	originalClient := evaluationHTTPClient
	defer func() { evaluationHTTPClient = originalClient }()

	tests := []struct {
		name   string
		status int
		body   string
		kind   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{}`, kind: compatibilityUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, body: `{}`, kind: compatibilityUnauthorized},
		{name: "not supported", status: http.StatusNotFound, body: `{}`, kind: compatibilityNotSupported},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`, kind: compatibilityFailed},
		{name: "incompatible", status: http.StatusOK, body: `{"success":true,"data":{}}`, kind: compatibilityIncompatible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: http.Header{}, Request: r}, nil
			})}
			_, err := fetchLocalCurrentSourcePage("https://example.com/api/v1/knowledge-bases?page=1&page_size=1", "secret", validateSourceKnowledgeBases)
			var sourceErr *sourceReadError
			if !errors.As(err, &sourceErr) || sourceErr.Kind != test.kind {
				t.Fatalf("error = %#v, want kind %q", err, test.kind)
			}
		})
	}
}

func TestWeKnoraChunkImportPreviewDeduplicatesAndImportsAtomically(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.create(createDatasetRequest{ID: "chunk-import", Name: "分块导入", Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	project.Passages = []Passage{
		{ID: 1, Text: "same text", Metadata: map[string]string{
			"source_type": "weknora-chunk", "source_profile_id": "local-source", "source_chunk_id": "chunk-same", "source_content_sha256": contentSHA256("same text"),
		}},
		{ID: 2, Text: "old changed text", Metadata: map[string]string{
			"source_type": "weknora-chunk", "source_profile_id": "local-source", "source_chunk_id": "chunk-changed", "source_content_sha256": contentSHA256("old changed text"),
		}},
	}
	project.NextPassageID = 3
	if err := store.save(project.ID, project); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveConnectionProfile("local-source", &ConnectionProfile{
		ID: "local-source", Name: "本地来源", Environment: "local", BaseURL: "http://127.0.0.1:8080", APIKey: "saved-key", AdapterID: localCurrentAdapterID,
	}); err != nil {
		t.Fatal(err)
	}

	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("X-API-Key") != "saved-key" {
			t.Fatalf("source request = %s key=%q", r.Method, r.Header.Get("X-API-Key"))
		}
		body := ""
		switch r.URL.Path {
		case "/api/v1/knowledge-bases/kb-1/knowledge":
			body = `{"success":true,"data":[{"id":"doc-1","knowledge_base_id":"kb-1","file_name":"manual.pdf","parse_status":"completed","enable_status":"enabled"}],"total":1,"page":1,"page_size":100}`
		case "/api/v1/chunks/doc-1":
			body = `{"success":true,"data":[` +
				`{"id":"chunk-new","knowledge_id":"doc-1","content":"new text","chunk_index":1,"is_enabled":true,"chunk_type":"text"},` +
				`{"id":"chunk-same","knowledge_id":"doc-1","content":"same text","chunk_index":2,"is_enabled":true,"chunk_type":"text"},` +
				`{"id":"chunk-changed","knowledge_id":"doc-1","content":"new changed text","chunk_index":3,"is_enabled":true,"chunk_type":"text"}` +
				`],"total":3,"page":1,"page_size":100}`
		default:
			t.Fatalf("unexpected source path %q", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	req := sourceImportRequest{
		ProfileID: "local-source", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-1",
		ChunkIDs: []string{"chunk-new", "chunk-same", "chunk-changed", "chunk-missing"}, ChangedPolicy: "skip",
	}
	preview, err := store.previewSourceImport(project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary != (sourceImportSummary{Selected: 4, Importable: 1, Existing: 1, Changed: 1, Invalid: 1}) {
		t.Fatalf("preview summary = %#v", preview.Summary)
	}
	result, err := store.importSourceChunks(project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Imported != 1 || result.Summary.Skipped != 3 || !reflect.DeepEqual(result.ImportedPassageIDs, []int64{3}) {
		t.Fatalf("import result = %#v", result)
	}
	if len(result.Project.Passages) != 3 || result.Project.Passages[2].Metadata["source_chunk_id"] != "chunk-new" || result.Project.Passages[2].Metadata["source_content_sha256"] != contentSHA256("new text") {
		t.Fatalf("imported passage = %#v", result.Project.Passages)
	}
	repeated, err := store.previewSourceImport(project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Summary.Importable != 0 || repeated.Summary.Existing != 2 || repeated.Summary.Changed != 1 || repeated.Summary.Invalid != 1 {
		t.Fatalf("repeated preview = %#v", repeated.Summary)
	}

	originalGenerationClient := generationHTTPClient
	generationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		content := `{"candidates":[{"question":"新增内容是什么？","answer":"新增内容是 new text。","answer_key_points":["new text"],"category":"事实类","difficulty":"easy","tags":["导入"],"answerable":true,"evidence_quotes":["new text"]}]}`
		body, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40},
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	defer func() { generationHTTPClient = originalGenerationClient }()
	profile, err := store.saveGenerationConnection("import-generator", &GenerationConnection{
		ID: "import-generator", Name: "导入闭环测试", BaseURL: "https://generator.test/v1", APIKey: "test-key", Model: "mock-model",
		TimeoutSeconds: 10, QuestionsPerPassage: 1, MaxOutputTokens: 512, MaxTotalTokens: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.startGenerationJob(project.ID, generationStartRequest{ConnectionID: profile.ID, PassageIDs: result.ImportedPassageIDs, QuestionsPerPassage: 1})
	if err != nil {
		t.Fatal(err)
	}
	job = waitGenerationJob(t, store, project.ID, job.ID)
	if job.Status != "succeeded" || len(job.Candidates) != 1 || !reflect.DeepEqual(job.Candidates[0].PassageIDs, []int64{3}) {
		t.Fatalf("generation after import = %#v", job)
	}
	candidate := job.Candidates[0]
	_, completedProject, err := store.reviewGenerationCandidates(project.ID, job.ID, generationReviewRequest{Items: []generationReviewItem{{
		CandidateID: candidate.ID, Action: "approve", Question: candidate.Question, Answer: candidate.Answer,
		AnswerKeyPoints: candidate.AnswerKeyPoints, Category: candidate.Category, Difficulty: candidate.Difficulty,
		Tags: candidate.Tags, EvidenceQuotes: candidate.EvidenceQuotes,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completedProject.Questions) != 1 || !reflect.DeepEqual(completedProject.Questions[0].RelevantPassageIDs, []int64{3}) {
		t.Fatalf("approved imported question = %#v", completedProject.Questions)
	}
	if report := validateProject(completedProject); !report.Valid {
		t.Fatalf("import-generation project validation = %#v", report.Issues)
	}
	zipPath, summary, err := store.exportWeKnora(completedProject)
	if err != nil {
		t.Fatal(err)
	}
	if summary.QuestionCount != 1 || summary.PassageCount != 3 {
		t.Fatalf("import-generation export summary = %#v", summary)
	}
	if info, err := os.Stat(zipPath); err != nil || info.Size() == 0 {
		t.Fatalf("import-generation ZIP = %q, info = %#v, err = %v", zipPath, info, err)
	}
}

func TestConnectionProfilesAndReadOnlyCompatibilityReport(t *testing.T) {
	const secret = "sk-compatibility-secret"
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("local-dev", &ConnectionProfile{
		ID:              "local-dev",
		Name:            "本地开发",
		Environment:     "local",
		BaseURL:         "http://127.0.0.1:8080",
		APIKey:          " " + secret + " ",
		AdapterID:       localCurrentAdapterID,
		DatasetID:       "default",
		KnowledgeBaseID: " kb-1 ",
		ChatModelID:     " chat-1 ",
		RerankModelID:   " rerank-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "http://127.0.0.1:8080/api/v1" {
		t.Fatalf("normalized base URL = %q", profile.BaseURL)
	}
	if profile.KnowledgeBaseID != "kb-1" || profile.ChatModelID != "chat-1" || profile.RerankModelID != "rerank-1" {
		t.Fatalf("saved resource IDs = %#v", profile)
	}
	persistedProfile, err := store.getConnectionProfile(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedProfile.APIKey != secret || persistedProfile.KnowledgeBaseID != "kb-1" || persistedProfile.ChatModelID != "chat-1" || persistedProfile.RerankModelID != "rerank-1" {
		t.Fatalf("persisted resource IDs = %#v", persistedProfile)
	}
	profileInfo, err := os.Stat(filepath.Join(store.connections, profile.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if profileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("connection profile permissions = %o", profileInfo.Mode().Perm())
	}
	if _, err := store.saveConnectionProfile("unsafe-production", &ConnectionProfile{
		ID: "unsafe-production", Name: "生产", Environment: "production",
		BaseURL: "http://example.com", AdapterID: localCurrentAdapterID,
	}); err == nil {
		t.Fatal("production HTTP profile should be rejected")
	}

	originalClient := evaluationHTTPClient
	evaluationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("X-API-Key") != secret {
			t.Errorf("compatibility request = %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("X-API-Key"))
		}
		body := `{"data":{}}`
		switch r.URL.Path {
		case "/api/v1/knowledge-bases":
			body = `{"success":true,"data":[]}`
		case "/api/v1/models":
			body = `{"success":true,"data":[]}`
		case "/api/v1/knowledge-bases/kb-1/knowledge":
			body = `{"success":true,"data":[{"id":"doc-1","knowledge_base_id":"kb-1","title":"测试文档"}],"total":1,"page":1,"page_size":1}`
		case "/api/v1/chunks/doc-1":
			body = `{"success":true,"data":[{"id":"chunk-1","knowledge_id":"doc-1","knowledge_base_id":"kb-1","content":"测试分块"}],"total":1,"page":1,"page_size":1}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
	defer func() { evaluationHTTPClient = originalClient }()

	report, err := store.checkCompatibility(compatibilityCheckRequest{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Passed != 7 || report.Summary.Warnings != 1 || len(report.Checks) != 8 {
		t.Fatalf("compatibility report = %#v", report)
	}
	if !report.Capabilities.ImportKnowledgeChunks {
		t.Fatalf("chunk import capability = %#v", report.Capabilities)
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reportJSON, []byte(secret)) || bytes.Contains(reportJSON, []byte("127.0.0.1")) {
		t.Fatalf("compatibility report leaked a secret or host: %s", reportJSON)
	}
}

func TestManualExportConnectionNeedsNoCredential(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("production-manual", &ConnectionProfile{
		ID: "production-manual", Name: "生产手工部署", Environment: "production",
		BaseURL: "https://must-not-be-persisted.example.com", APIKey: "must-not-be-persisted-secret", AdapterID: manualExportAdapterID,
		DatasetID: "production-dataset-v1", KnowledgeBaseID: "must-not-be-persisted-kb",
		ChatModelID: "must-not-be-persisted-chat", RerankModelID: "must-not-be-persisted-rerank",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "" || profile.APIKey != "" || profile.KnowledgeBaseID != "" || profile.ChatModelID != "" || profile.RerankModelID != "" {
		t.Fatalf("manual profile retained remote-only fields = %#v", profile)
	}
	report, err := store.checkCompatibility(compatibilityCheckRequest{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.NotSupported != 1 || len(report.Checks) != 1 {
		t.Fatalf("manual report = %#v", report)
	}
	defaultProduction, err := store.saveConnectionProfile("production-default", &ConnectionProfile{
		ID: "production-default", Name: "生产默认配置", Environment: "production", DatasetID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if defaultProduction.AdapterID != manualExportAdapterID || defaultProduction.BaseURL != "" {
		t.Fatalf("production profile without explicit adapter must default to manual-export: %#v", defaultProduction)
	}
}

func TestManualEvaluationResultImport(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := validProject()
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	project, err = store.get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, exported, err := store.exportWeKnora(project)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("production-manual", &ConnectionProfile{
		ID: "production-manual", Name: "生产手工评测", Environment: "production",
		AdapterID: manualExportAdapterID, DatasetID: "production-dataset-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.startEvaluation(project.ID, evaluationStartRequest{
		ConnectionProfileID: profile.ID,
		ExportID:            exported.ID,
		DatasetDeployed:     true,
		KnowledgeBaseID:     "production-kb-reference",
		ChatModelID:         "production-chat-reference",
		RerankModelID:       "production-rerank-reference",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.AdapterID != manualExportAdapterID || run.Status != EvaluationTaskPending || run.ResultSource != "external_pending" || run.DatasetID != profile.DatasetID || run.ExportSHA256 != exported.SHA256 {
		t.Fatalf("manual run = %#v", run)
	}
	if run.KnowledgeBaseID != "" || run.ChatModelID != "" || run.RerankModelID != "" {
		t.Fatalf("manual run retained remote-only fields = %#v", run)
	}
	if !reflect.DeepEqual(run.ExportQuestionIDs, []int64{1, 2}) || run.ExportPassageIDMap[0] != 3 || run.ExportPassageIDMap[1] != 20 {
		t.Fatalf("manual run export provenance = %#v", run)
	}
	if _, err := store.importEvaluationResult(project.ID, run.ID, evaluationResultImportRequest{
		Status: "success", RetrievalMetrics: map[string]float64{"bad metric": 1},
	}); err == nil {
		t.Fatal("invalid metric name should be rejected")
	}
	completed, err := store.importEvaluationResult(project.ID, run.ID, evaluationResultImportRequest{
		Status:            "success",
		Total:             2,
		Finished:          2,
		RetrievalMetrics:  map[string]float64{"precision": 0.75, "recall": 1},
		GenerationMetrics: map[string]float64{"rouge1": 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != EvaluationTaskSucceeded || completed.ResultSource != "external_import" || completed.Metric == nil || completed.Metric.RetrievalMetrics["precision"] != 0.75 {
		t.Fatalf("completed manual run = %#v", completed)
	}
	persisted, err := store.getEvaluationRun(project.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != EvaluationTaskSucceeded || persisted.Finished != 2 {
		t.Fatalf("persisted manual run = %#v", persisted)
	}
	if _, err := store.importEvaluationResult(project.ID, run.ID, evaluationResultImportRequest{Status: "success"}); err == nil {
		t.Fatal("completed manual run should reject result replacement")
	}
}

func TestExternalResultPreviewImportAndCorrection(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	project := validProject()
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	project, _ = store.get(project.ID)
	_, exported, err := store.exportWeKnora(project)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("production-manual", &ConnectionProfile{
		ID: "production-manual", Name: "生产手工评测", Environment: "production",
		AdapterID: manualExportAdapterID, DatasetID: "production-dataset-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	run, err := store.startEvaluation(project.ID, evaluationStartRequest{
		ConnectionProfileID: profile.ID, ExportID: exported.ID, DatasetDeployed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	content := `{
  "schema_version": 1,
  "status": "success",
  "summary": {"total": 2, "finished": 1, "retrieval_metrics": {"precision": 1}},
  "items": [{"question_id": 1, "actual_answer": "长按重置键十秒。", "retrieved_passage_ids": [0], "retrieved_documents": ["产品手册"], "latency_ms": 120}]
}`
	preview, err := store.previewExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "json", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Valid || preview.MatchedCount != 1 || !reflect.DeepEqual(preview.MissingIDs, []int64{2}) || preview.CreatesRevision {
		t.Fatalf("preview = %#v", preview)
	}
	completed, err := store.importExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "json", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if completed.ID != run.ID || completed.Status != EvaluationTaskSucceeded || len(completed.ExternalItems) != 1 || len(completed.ExternalResultSHA256) != 64 || completed.ExternalResultSchemaVersion != 1 {
		t.Fatalf("completed external run = %#v", completed)
	}

	invalidCSV := "question_id,actual_answer,retrieved_passage_ids\n1,答案,0\n1,重复,0\n999,未知,0\n"
	invalidPreview, err := store.previewExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "csv", Content: invalidCSV})
	if err != nil {
		t.Fatal(err)
	}
	if invalidPreview.Valid || !reflect.DeepEqual(invalidPreview.DuplicateIDs, []int64{1}) || !reflect.DeepEqual(invalidPreview.UnknownIDs, []int64{999}) || !invalidPreview.CreatesRevision {
		t.Fatalf("invalid CSV preview = %#v", invalidPreview)
	}
	if _, err := store.importExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "csv", Content: invalidCSV}); err == nil {
		t.Fatal("invalid CSV should not be imported")
	}

	now = now.Add(time.Second)
	validCSV := "question_id,actual_answer,retrieved_passage_ids,retrieved_documents,latency_ms,error_message,answer_judgement,answer_score,assessment_source,review_comment\n1,长按重置键十秒,0,产品手册,100,,correct,1,人工审核,覆盖要点\n2,检查网络,1,故障手册,200,,partial,0.5,规则评分器,缺少电源检查\n"
	revision, err := store.importExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "csv", Content: validCSV})
	if err != nil {
		t.Fatal(err)
	}
	if revision.ID == run.ID || revision.CorrectionOfRunID != run.ID || revision.ExternalResultFormat != "csv" || len(revision.ExternalItems) != 2 || revision.ExternalItems[1].AnswerJudgement != "partial" || revision.ExternalItems[1].AnswerScore == nil || *revision.ExternalItems[1].AnswerScore != 0.5 || revision.ExternalItems[1].AssessmentSource != "规则评分器" {
		t.Fatalf("revision = %#v", revision)
	}
	original, err := store.getEvaluationRun(project.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if original.ExternalResultFormat != "json" || len(original.ExternalItems) != 1 {
		t.Fatalf("original run was overwritten = %#v", original)
	}
}

func TestOfflineEvaluationReportAndComparison(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	project := validProject()
	answerable := true
	project.Questions[0].Category = "故障"
	project.Questions[0].Difficulty = "普通"
	project.Questions[0].Tags = []string{"网络"}
	project.Questions[0].Answerable = &answerable
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	project, _ = store.get(project.ID)
	_, exported, err := store.exportWeKnora(project)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveConnectionProfile("report-manual", &ConnectionProfile{
		ID: "report-manual", Name: "报告测试", Environment: "production", AdapterID: manualExportAdapterID, DatasetID: "report-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	createRun := func(content string) *EvaluationRun {
		now = now.Add(time.Second)
		run, startErr := store.startEvaluation(project.ID, evaluationStartRequest{ConnectionProfileID: profile.ID, ExportID: exported.ID, DatasetDeployed: true})
		if startErr != nil {
			t.Fatal(startErr)
		}
		completed, importErr := store.importExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "json", Content: content})
		if importErr != nil {
			t.Fatal(importErr)
		}
		return completed
	}
	base := createRun(`{"schema_version":1,"status":"success","items":[{"question_id":1,"retrieved_passage_ids":[],"actual_answer":"错误答案"},{"question_id":2,"retrieved_passage_ids":[1],"actual_answer":"正确答案","answer_judgement":"correct","answer_score":1,"assessment_source":"人工审核","review_comment":"覆盖标准答案"}]}`)
	report, err := store.buildEvaluationReport(project.ID, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Total != 2 || report.Summary.Executed != 2 || report.Summary.ZeroRecall != 1 || report.Metrics.Computable != 2 {
		t.Fatalf("report summary = %#v, metrics = %#v", report.Summary, report.Metrics)
	}
	if report.Metrics.Precision != 0.5 || report.Metrics.Recall != 0.5 || report.Metrics.Hit != 0.5 || report.Metrics.MRR != 0.5 {
		t.Fatalf("report metrics = %#v", report.Metrics)
	}
	if report.AnswerAssessment.Reviewed != 1 || report.AnswerAssessment.Correct != 1 || report.AnswerAssessment.Unreviewed != 1 || report.AnswerAssessment.Scored != 1 || report.AnswerAssessment.AverageScore != 1 {
		t.Fatalf("answer assessment = %#v", report.AnswerAssessment)
	}
	if report.Items[0].State != "completed" || report.Items[0].Recall != 0 || report.Items[1].Recall != 1 {
		t.Fatalf("report items = %#v", report.Items)
	}
	if len(report.Groups) == 0 || report.AnswerAssessmentNote == "" || base.QuestionMetadata[2].GoldSources[0] != "故障手册" {
		t.Fatalf("report metadata missing: %#v %#v", report.Groups, base.QuestionMetadata)
	}
	feedback, err := store.createEvaluationFeedbackDataset(project.ID, base.ID, evaluationFeedbackRequest{
		ID: "customer-service-feedback", Name: "客服失败样本", Version: "0.2.0", QuestionIDs: []int64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback.Questions) != 1 || len(feedback.Passages) != 1 || feedback.Questions[0].SourceRunID != base.ID || feedback.Questions[0].SourceDatasetVersion != project.Version || feedback.Questions[0].FailureReason != "零召回" {
		t.Fatalf("feedback project = %#v", feedback)
	}
	if feedback.Passages[0].Source != "产品手册" || !reflect.DeepEqual(feedback.Questions[0].RelevantPassageIDs, []int64{3}) {
		t.Fatalf("feedback relations = %#v / %#v", feedback.Passages, feedback.Questions)
	}
	if _, err := store.createEvaluationFeedbackDataset(project.ID, base.ID, evaluationFeedbackRequest{
		ID: "must-not-exist", Name: "不应创建", Version: "0.2.0", QuestionIDs: []int64{2},
	}); err == nil || !strings.Contains(err.Error(), "不能回流") {
		t.Fatalf("successful question should be rejected, err = %v", err)
	}
	invalidAssessment := `{"schema_version":1,"status":"success","items":[{"question_id":1,"retrieved_passage_ids":[0],"answer_judgement":"maybe","answer_score":1.5}]}`
	invalidPreview, err := store.previewExternalEvaluationResult(project.ID, base.ID, externalResultContentRequest{Format: "json", Content: invalidAssessment})
	if err != nil || invalidPreview.Valid || len(invalidPreview.Issues) < 2 {
		t.Fatalf("invalid answer assessment preview = %#v, err = %v", invalidPreview, err)
	}
	html, err := renderEvaluationReportHTML(report)
	if err != nil || !strings.Contains(string(html), "离线评测报告") || strings.Contains(string(html), "<script") {
		t.Fatalf("html report invalid: %v %s", err, html)
	}
	failures, err := renderEvaluationFailuresCSV(report)
	if err != nil || !strings.Contains(string(failures), "错误答案") || strings.Contains(string(failures), "正确答案") {
		t.Fatalf("failure CSV invalid: %v %s", err, failures)
	}

	target := createRun(`{"schema_version":1,"status":"success","items":[{"question_id":1,"retrieved_passage_ids":[0],"actual_answer":"修正答案"}]}`)
	comparison, err := store.compareEvaluationReports(project.ID, base.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Summary.Improved != 1 || comparison.Summary.NewlyMissing != 1 {
		t.Fatalf("comparison = %#v", comparison)
	}
}

func TestOfflineLifecycleEndToEnd(t *testing.T) {
	root := t.TempDir()
	store, err := newProjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	project := newDatasetProject(createDatasetRequest{ID: "offline-lifecycle", Name: "离线闭环验收", Version: "1.0.0"}, now)
	answerable := true
	for index := int64(1); index <= 10; index++ {
		project.Passages = append(project.Passages, Passage{ID: index * 10, Text: fmt.Sprintf("这是问题 %d 对应的黄金知识库语料正文，用于离线闭环验收。", index), Source: fmt.Sprintf("验收文档-%d", index), ReviewState: "approved"})
		project.Questions = append(project.Questions, Question{
			ID: index, Text: fmt.Sprintf("验收问题 %d 应该如何回答？", index), Answer: fmt.Sprintf("标准答案 %d", index), RelevantPassageIDs: []int64{index * 10},
			Category: "闭环验收", Difficulty: "普通", Tags: []string{"离线"}, ReviewState: "approved", Answerable: &answerable,
			AnswerKeyPoints: []string{fmt.Sprintf("要点 %d", index)}, DatasetVersion: "1.0.0", AnnotationSource: "自动化脱敏 fixture",
		})
	}
	project.NextPassageID = 101
	project.NextQuestionID = 11
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	project, _ = store.get(project.ID)
	if report := validateProject(project); !report.Valid || report.Statistics.QuestionCount != 10 {
		t.Fatalf("validation = %#v", report)
	}
	if _, err := store.createSnapshot(project.ID); err != nil {
		t.Fatal(err)
	}
	_, exported, err := store.exportWeKnora(project)
	if err != nil {
		t.Fatal(err)
	}
	if exported.QuestionCount != 10 || exported.PassageCount != 10 || exported.QrelCount != 10 || len(exported.SHA256) != 64 || exported.ContractProfileID != weKnoraFiveParquetProfileID {
		t.Fatalf("export = %#v", exported)
	}
	profile, err := store.saveConnectionProfile("offline-production", &ConnectionProfile{ID: "offline-production", Name: "离线生产验收", Environment: "production", AdapterID: manualExportAdapterID, DatasetID: "offline-v1"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	run, err := store.startEvaluation(project.ID, evaluationStartRequest{ConnectionProfileID: profile.ID, ExportID: exported.ID, DatasetDeployed: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "external-results", "offline-lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.importExternalEvaluationResult(project.ID, run.ID, externalResultContentRequest{Format: "json", Content: string(fixture)})
	if err != nil {
		t.Fatal(err)
	}
	offlineReport, err := store.buildEvaluationReport(project.ID, completed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offlineReport.Summary.Total != 10 || offlineReport.Summary.Executed != 10 || offlineReport.Summary.Failed != 1 || offlineReport.Summary.Uncomputable != 1 || offlineReport.Summary.ZeroRecall != 4 || offlineReport.Metrics.Computable != 9 {
		t.Fatalf("offline report = %#v / %#v", offlineReport.Summary, offlineReport.Metrics)
	}
	feedback, err := store.createEvaluationFeedbackDataset(project.ID, completed.ID, evaluationFeedbackRequest{ID: "offline-lifecycle-feedback", Name: "离线失败样本", Version: "1.1.0", QuestionIDs: []int64{2, 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback.Questions) != 2 || len(feedback.Passages) != 2 {
		t.Fatalf("feedback = %#v", feedback)
	}

	restarted, err := newProjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	reloadedProject, err := restarted.get(project.ID)
	if err != nil || len(reloadedProject.Questions) != 10 {
		t.Fatalf("reloaded project = %#v, err = %v", reloadedProject, err)
	}
	reloadedRun, err := restarted.getEvaluationRun(project.ID, completed.ID)
	if err != nil || len(reloadedRun.ExternalItems) != 10 {
		t.Fatalf("reloaded run = %#v, err = %v", reloadedRun, err)
	}
	reloadedReport, err := restarted.buildEvaluationReport(project.ID, completed.ID)
	if err != nil || reloadedReport.Summary.ZeroRecall != 4 {
		t.Fatalf("reloaded report = %#v, err = %v", reloadedReport, err)
	}
	if _, err := restarted.get(feedback.ID); err != nil {
		t.Fatalf("reloaded feedback: %v", err)
	}
}

func TestServerDatasetCRUDAndValidation(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := newStudioServer(store, webAssets)
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"id":"api-eval","name":"API 测试","version":"0.1.0"}`)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/datasets", body)
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/datasets/api-eval", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data DatasetProject `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ID != "api-eval" {
		t.Fatalf("project id = %q", payload.Data.ID)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/datasets/api-eval/validate", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestServerStaticAssetsDisableBrowserCache(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := newStudioServer(store, webAssets)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	request.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("static status = %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !strings.Contains(response.Body.String(), "generation-test-prompt") {
		t.Fatal("served index does not contain the test prompt field")
	}
}

func TestServerCSVImport(t *testing.T) {
	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.create(createDatasetRequest{ID: "csv-api", Name: "CSV API", Version: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	server, err := newStudioServer(store, webAssets)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(csvImportRequest{
		Kind: "passages",
		CSV:  "text,source\n这是一条长度足够的测试语料文本,接口测试\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/datasets/csv-api/import/csv", bytes.NewReader(body))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}
	project, err := store.get("csv-api")
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Passages) != 1 || project.Passages[0].Source != "接口测试" {
		t.Fatalf("project passages = %#v", project.Passages)
	}
}

func TestServerWeKnoraImportSnapshotAndExportHistory(t *testing.T) {
	sourceStore, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	zipPath, _, err := sourceStore.exportWeKnora(validProject())
	if err != nil {
		t.Fatal(err)
	}
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := newStudioServer(store, webAssets)
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := json.Marshal(weKnoraImportRequest{
		ID:        "api-zip-import",
		Name:      "API ZIP 导入",
		Version:   "1.0.0",
		ZIPBase64: base64.StdEncoding.EncodeToString(zipData),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/import/weknora", bytes.NewReader(requestBody))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/datasets/api-zip-import/snapshots", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("snapshot status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/datasets/api-zip-import/export/weknora", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Export-ID") == "" {
		t.Fatalf("export status = %d, headers = %#v, body = %s", response.Code, response.Header(), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/datasets/api-zip-import/exports", nil)
	request.Host = "127.0.0.1"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "file_name") {
		t.Fatalf("history status = %d, body = %s", response.Code, response.Body.String())
	}
}
