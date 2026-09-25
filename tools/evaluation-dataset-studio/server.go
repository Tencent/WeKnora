package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const maxJSONRequestSize = 70 << 20

type studioServer struct {
	store   *projectStore
	handler http.Handler
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Report  *ValidationReport `json:"report,omitempty"`
}

func newStudioServer(store *projectStore, assets embed.FS) (*studioServer, error) {
	web, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}
	server := &studioServer{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/datasets", server.handleListDatasets)
	mux.HandleFunc("GET /api/settings", server.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", server.handleSaveSettings)
	mux.HandleFunc("GET /api/configuration-references/{type}/{id}", server.handleConfigurationReferences)
	mux.HandleFunc("POST /api/datasets", server.handleCreateDataset)
	mux.HandleFunc("GET /api/datasets/{id}", server.handleGetDataset)
	mux.HandleFunc("PUT /api/datasets/{id}", server.handleSaveDataset)
	mux.HandleFunc("DELETE /api/datasets/{id}", server.handleDeleteDataset)
	mux.HandleFunc("POST /api/datasets/{id}/validate", server.handleValidateDataset)
	mux.HandleFunc("POST /api/datasets/{id}/import/csv", server.handleImportCSV)
	mux.HandleFunc("POST /api/datasets/{id}/export/weknora", server.handleExportDataset)
	mux.HandleFunc("GET /api/datasets/{id}/exports", server.handleListExports)
	mux.HandleFunc("GET /api/datasets/{id}/exports/{exportID}", server.handleDownloadExport)
	mux.HandleFunc("PUT /api/datasets/{id}/exports/{exportID}/deployment", server.handleUpdateExportDeployment)
	mux.HandleFunc("GET /api/export-contracts", server.handleExportContracts)
	mux.HandleFunc("GET /api/datasets/{id}/snapshots", server.handleListSnapshots)
	mux.HandleFunc("POST /api/datasets/{id}/snapshots", server.handleCreateSnapshot)
	mux.HandleFunc("POST /api/datasets/{id}/snapshots/{snapshotID}/restore", server.handleRestoreSnapshot)
	mux.HandleFunc("GET /api/datasets/{id}/evaluations", server.handleListEvaluations)
	mux.HandleFunc("POST /api/datasets/{id}/evaluations", server.handleStartEvaluation)
	mux.HandleFunc("POST /api/datasets/{id}/evaluations/{runID}/poll", server.handlePollEvaluation)
	mux.HandleFunc("PUT /api/datasets/{id}/evaluations/{runID}/result", server.handleImportEvaluationResult)
	mux.HandleFunc("POST /api/datasets/{id}/evaluations/{runID}/external-result/preview", server.handlePreviewExternalEvaluationResult)
	mux.HandleFunc("POST /api/datasets/{id}/evaluations/{runID}/external-result/import", server.handleImportExternalEvaluationResult)
	mux.HandleFunc("GET /api/datasets/{id}/evaluations/{runID}/report", server.handleEvaluationReport)
	mux.HandleFunc("GET /api/datasets/{id}/evaluations/{runID}/report.html", server.handleEvaluationReportHTML)
	mux.HandleFunc("GET /api/datasets/{id}/evaluations/{runID}/failures.csv", server.handleEvaluationFailuresCSV)
	mux.HandleFunc("POST /api/datasets/{id}/evaluations/{runID}/feedback-dataset", server.handleCreateEvaluationFeedbackDataset)
	mux.HandleFunc("GET /api/datasets/{id}/evaluation-comparison", server.handleEvaluationComparison)
	mux.HandleFunc("POST /api/evaluation-resources", server.handleEvaluationResources)
	mux.HandleFunc("GET /api/connections", server.handleListConnections)
	mux.HandleFunc("PUT /api/connections/{id}", server.handleSaveConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", server.handleDeleteConnection)
	mux.HandleFunc("POST /api/compatibility-checks", server.handleCompatibilityCheck)
	mux.HandleFunc("GET /api/source-profiles/{id}/knowledge-bases", server.handleSourceKnowledgeBases)
	mux.HandleFunc("GET /api/source-profiles/{id}/knowledge-bases/{kbID}/knowledge", server.handleSourceKnowledge)
	mux.HandleFunc("GET /api/source-profiles/{id}/knowledge/{knowledgeID}/chunks", server.handleSourceChunks)
	mux.HandleFunc("POST /api/datasets/{id}/passages/import-weknora-preview", server.handlePreviewSourceImport)
	mux.HandleFunc("POST /api/datasets/{id}/passages/import-weknora", server.handleImportSourceChunks)
	mux.HandleFunc("GET /api/generation-connections", server.handleListGenerationConnections)
	mux.HandleFunc("PUT /api/generation-connections/{id}", server.handleSaveGenerationConnection)
	mux.HandleFunc("DELETE /api/generation-connections/{id}", server.handleDeleteGenerationConnection)
	mux.HandleFunc("POST /api/generation-connections/{id}/test", server.handleTestGenerationConnection)
	mux.HandleFunc("GET /api/datasets/{id}/generation-jobs", server.handleListGenerationJobs)
	mux.HandleFunc("POST /api/datasets/{id}/generation-jobs", server.handleStartGenerationJob)
	mux.HandleFunc("GET /api/datasets/{id}/generation-jobs/{jobID}", server.handleGetGenerationJob)
	mux.HandleFunc("POST /api/datasets/{id}/generation-jobs/{jobID}/cancel", server.handleCancelGenerationJob)
	mux.HandleFunc("POST /api/datasets/{id}/generation-jobs/{jobID}/review", server.handleReviewGenerationCandidates)
	mux.HandleFunc("GET /api/datasets/{id}/backup", server.handleBackupDataset)
	mux.HandleFunc("POST /api/import/project", server.handleImportProject)
	mux.HandleFunc("POST /api/import/weknora", server.handleImportWeKnora)
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	server.handler = server.securityHeaders(server.localOnly(mux))
	return server, nil
}

func (s *studioServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *studioServer) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.store.getWorkspaceSettings()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "settings_read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": settings})
}

func (s *studioServer) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var settings WorkspaceSettings
	if err := decodeJSONRequest(w, r, &settings); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	saved, err := s.store.saveWorkspaceSettings(settings)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "settings_save_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": saved})
}

func (s *studioServer) handleConfigurationReferences(w http.ResponseWriter, r *http.Request) {
	refs, err := s.store.configurationReferences(r.PathValue("type"), r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "configuration_references_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": refs})
}

func (s *studioServer) handleListDatasets(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.list()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *studioServer) handleCreateDataset(w http.ResponseWriter, r *http.Request) {
	var req createDatasetRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	project, err := s.store.create(req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已存在") {
			status = http.StatusConflict
		}
		writeAPIError(w, status, "create_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": project})
}

func (s *studioServer) handleGetDataset(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": project})
}

func (s *studioServer) handleSaveDataset(w http.ResponseWriter, r *http.Request) {
	var project DatasetProject
	if err := decodeJSONRequest(w, r, &project); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if err := s.store.save(r.PathValue("id"), &project); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": &project})
}

func (s *studioServer) handleDeleteDataset(w http.ResponseWriter, r *http.Request) {
	if err := s.store.delete(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *studioServer) handleValidateDataset(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": validateProject(project)})
}

func (s *studioServer) handleImportCSV(w http.ResponseWriter, r *http.Request) {
	var req csvImportRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	project, err := s.store.get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	summary, err := importProjectCSV(project, req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "csv_import_failed", err.Error(), nil)
		return
	}
	if err := s.store.save(project.ID, project); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"project": project,
		"summary": summary,
	}})
}

func sourcePageFromRequest(r *http.Request) SourcePageRequest {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return normalizeSourcePage(SourcePageRequest{Page: page, PageSize: pageSize})
}

func writeSourceError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	code := "source_read_failed"
	var sourceErr *sourceReadError
	if errors.As(err, &sourceErr) {
		switch sourceErr.Kind {
		case compatibilityUnauthorized:
			status, code = http.StatusUnauthorized, "source_unauthorized"
		case compatibilityNotSupported:
			status, code = http.StatusNotImplemented, "source_not_supported"
		case compatibilityIncompatible:
			status, code = http.StatusBadGateway, "source_incompatible"
		}
	} else if errors.Is(err, errDatasetNotFound) {
		status, code = http.StatusNotFound, "dataset_not_found"
	} else if strings.Contains(err.Error(), "不能为空") || strings.Contains(err.Error(), "只能") || strings.Contains(err.Error(), "最多") || strings.Contains(err.Error(), "没有可导入") {
		status, code = http.StatusBadRequest, "source_import_invalid"
	}
	writeAPIError(w, status, code, err.Error(), nil)
}

func (s *studioServer) handleSourceKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	profile, adapter, err := s.store.chunkSourceProfile(r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	result, err := adapter.ListSourceKnowledgeBases(profile.BaseURL, profile.APIKey, sourcePageFromRequest(r))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *studioServer) handleSourceKnowledge(w http.ResponseWriter, r *http.Request) {
	profile, adapter, err := s.store.chunkSourceProfile(r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	result, err := adapter.ListSourceKnowledge(profile.BaseURL, profile.APIKey, r.PathValue("kbID"), sourcePageFromRequest(r))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *studioServer) handleSourceChunks(w http.ResponseWriter, r *http.Request) {
	profile, adapter, err := s.store.chunkSourceProfile(r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	result, err := adapter.ListSourceChunks(profile.BaseURL, profile.APIKey, r.PathValue("knowledgeID"), sourcePageFromRequest(r))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *studioServer) handlePreviewSourceImport(w http.ResponseWriter, r *http.Request) {
	var req sourceImportRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	preview, err := s.store.previewSourceImport(r.PathValue("id"), req)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": preview})
}

func (s *studioServer) handleImportSourceChunks(w http.ResponseWriter, r *http.Request) {
	var req sourceImportRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	result, err := s.store.importSourceChunks(r.PathValue("id"), req)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *studioServer) handleExportDataset(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	path, summary, err := s.store.exportWeKnora(project)
	if err != nil {
		var validationErr *validationFailedError
		if errors.As(err, &validationErr) {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), &validationErr.Report)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "export_failed", err.Error(), nil)
		return
	}
	w.Header().Set("X-Export-ID", summary.ID)
	serveZIPDownload(w, path, summary)
}

func (s *studioServer) handleListExports(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.listExports(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *studioServer) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	path, item, err := s.store.exportHistoryPath(r.PathValue("id"), r.PathValue("exportID"))
	if err != nil {
		if strings.Contains(err.Error(), "不存在") {
			writeAPIError(w, http.StatusNotFound, "export_not_found", err.Error(), nil)
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_export", err.Error(), nil)
		return
	}
	serveZIPDownload(w, path, item)
}

func (s *studioServer) handleUpdateExportDeployment(w http.ResponseWriter, r *http.Request) {
	var req exportDeploymentRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	item, err := s.store.updateExportDeployment(r.PathValue("id"), r.PathValue("exportID"), req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, "deployment_update_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item})
}

func (s *studioServer) handleExportContracts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": []ExportContractProfile{weKnoraFiveParquetProfile}})
}

func serveZIPDownload(w http.ResponseWriter, path string, summary ExportHistoryItem) {
	file, err := os.Open(path)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "export_open_failed", err.Error(), nil)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "export_stat_failed", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": summary.FileName}))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Dataset-Questions", strconv.Itoa(summary.QuestionCount))
	w.Header().Set("X-Dataset-Passages", strconv.Itoa(summary.PassageCount))
	w.Header().Set("X-Dataset-Qrels", strconv.Itoa(summary.QrelCount))
	_, _ = io.Copy(w, file)
}

func (s *studioServer) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.listSnapshots(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *studioServer) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.createSnapshot(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": item})
}

func (s *studioServer) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.restoreSnapshot(r.PathValue("id"), r.PathValue("snapshotID"))
	if err != nil {
		if strings.Contains(err.Error(), "快照不存在") {
			writeAPIError(w, http.StatusNotFound, "snapshot_not_found", err.Error(), nil)
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": project})
}

func (s *studioServer) handleListEvaluations(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.listEvaluations(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": runs})
}

func (s *studioServer) handleStartEvaluation(w http.ResponseWriter, r *http.Request) {
	var req evaluationStartRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	run, err := s.store.startEvaluation(r.PathValue("id"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": run})
}

func (s *studioServer) handlePollEvaluation(w http.ResponseWriter, r *http.Request) {
	var req evaluationPollRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	run, err := s.store.pollEvaluation(r.PathValue("id"), r.PathValue("runID"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": run})
}

func (s *studioServer) handleImportEvaluationResult(w http.ResponseWriter, r *http.Request) {
	var req evaluationResultImportRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	run, err := s.store.importEvaluationResult(r.PathValue("id"), r.PathValue("runID"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": run})
}

func (s *studioServer) handlePreviewExternalEvaluationResult(w http.ResponseWriter, r *http.Request) {
	var req externalResultContentRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	preview, err := s.store.previewExternalEvaluationResult(r.PathValue("id"), r.PathValue("runID"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": preview})
}

func (s *studioServer) handleImportExternalEvaluationResult(w http.ResponseWriter, r *http.Request) {
	var req externalResultContentRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	run, err := s.store.importExternalEvaluationResult(r.PathValue("id"), r.PathValue("runID"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": run})
}

func (s *studioServer) handleEvaluationReport(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.buildEvaluationReport(r.PathValue("id"), r.PathValue("runID"))
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		data, err := marshalEvaluationReport(report)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "report_failed", err.Error(), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "evaluation-report-" + report.RunID + ".json"}))
		_, _ = w.Write(data)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": report})
}

func (s *studioServer) handleEvaluationReportHTML(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.buildEvaluationReport(r.PathValue("id"), r.PathValue("runID"))
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	data, err := renderEvaluationReportHTML(report)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "evaluation-report-" + report.RunID + ".html"}))
	_, _ = w.Write(data)
}

func (s *studioServer) handleEvaluationFailuresCSV(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.buildEvaluationReport(r.PathValue("id"), r.PathValue("runID"))
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	data, err := renderEvaluationFailuresCSV(report)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "evaluation-failures-" + report.RunID + ".csv"}))
	_, _ = w.Write(data)
}

func (s *studioServer) handleCreateEvaluationFeedbackDataset(w http.ResponseWriter, r *http.Request) {
	var req evaluationFeedbackRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	project, err := s.store.createEvaluationFeedbackDataset(r.PathValue("id"), r.PathValue("runID"), req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": project})
}

func (s *studioServer) handleEvaluationComparison(w http.ResponseWriter, r *http.Request) {
	comparison, err := s.store.compareEvaluationReports(r.PathValue("id"), r.URL.Query().Get("base"), r.URL.Query().Get("target"))
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": comparison})
}

func (s *studioServer) handleEvaluationResources(w http.ResponseWriter, r *http.Request) {
	var req evaluationResourcesRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	resources, err := loadEvaluationResources(req)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": resources})
}

func (s *studioServer) handleListConnections(w http.ResponseWriter, _ *http.Request) {
	profiles, err := s.store.listConnectionProfiles()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "connection_list_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": profiles})
}

func (s *studioServer) handleSaveConnection(w http.ResponseWriter, r *http.Request) {
	var profile ConnectionProfile
	if err := decodeJSONRequest(w, r, &profile); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	saved, err := s.store.saveConnectionProfile(r.PathValue("id"), &profile)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "connection_save_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": saved})
}

func (s *studioServer) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.store.deleteConnectionProfile(r.PathValue("id")); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, "connection_delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *studioServer) handleListGenerationConnections(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.listGenerationConnections()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "generation_connection_list_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *studioServer) handleSaveGenerationConnection(w http.ResponseWriter, r *http.Request) {
	var profile GenerationConnection
	if err := decodeJSONRequest(w, r, &profile); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	saved, err := s.store.saveGenerationConnection(r.PathValue("id"), &profile)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "generation_connection_save_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": saved})
}

func (s *studioServer) handleDeleteGenerationConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.store.deleteGenerationConnection(r.PathValue("id")); err != nil {
		writeAPIError(w, http.StatusBadRequest, "generation_connection_delete_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *studioServer) handleTestGenerationConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.store.testGenerationConnection(r.PathValue("id")); err != nil {
		writeAPIError(w, http.StatusBadGateway, "generation_connection_test_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"ok": true}})
}

func (s *studioServer) handleListGenerationJobs(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.listGenerationJobs(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *studioServer) handleStartGenerationJob(w http.ResponseWriter, r *http.Request) {
	var req generationStartRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	job, err := s.store.startGenerationJob(r.PathValue("id"), req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "generation_start_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": job})
}

func (s *studioServer) handleGetGenerationJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.getGenerationJob(r.PathValue("id"), r.PathValue("jobID"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "generation_job_not_found", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": job})
}

func (s *studioServer) handleCancelGenerationJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.cancelGenerationJob(r.PathValue("id"), r.PathValue("jobID"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "generation_cancel_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": job})
}

func (s *studioServer) handleReviewGenerationCandidates(w http.ResponseWriter, r *http.Request) {
	var req generationReviewRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	job, project, err := s.store.reviewGenerationCandidates(r.PathValue("id"), r.PathValue("jobID"), req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "generation_review_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"job": job, "project": project}})
}

func (s *studioServer) handleCompatibilityCheck(w http.ResponseWriter, r *http.Request) {
	var req compatibilityCheckRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	report, err := s.store.checkCompatibility(req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, "compatibility_check_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": report})
}

func writeEvaluationError(w http.ResponseWriter, err error) {
	var validationErr *validationFailedError
	if errors.As(err, &validationErr) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), &validationErr.Report)
		return
	}
	var remoteErr *remoteEvaluationError
	if errors.As(err, &remoteErr) {
		writeAPIError(w, http.StatusBadGateway, "weknora_request_failed", err.Error(), nil)
		return
	}
	if errors.Is(err, errDatasetNotFound) || strings.Contains(err.Error(), "评测记录不存在") {
		writeAPIError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	}
	writeAPIError(w, http.StatusBadRequest, "evaluation_failed", err.Error(), nil)
}

func (s *studioServer) handleBackupDataset(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": project.ID + "-project.json",
	}))
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(project)
}

func (s *studioServer) handleImportProject(w http.ResponseWriter, r *http.Request) {
	var project DatasetProject
	if err := decodeJSONRequest(w, r, &project); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if err := s.store.importProject(&project); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已存在") {
			status = http.StatusConflict
		}
		writeAPIError(w, status, "import_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": &project})
}

func (s *studioServer) handleImportWeKnora(w http.ResponseWriter, r *http.Request) {
	var req weKnoraImportRequest
	if err := decodeJSONRequest(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	project, summary, err := s.store.importWeKnora(req)
	if err != nil {
		var validationErr *validationFailedError
		if errors.As(err, &validationErr) {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), &validationErr.Report)
			return
		}
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "已存在") {
			status = http.StatusConflict
		}
		writeAPIError(w, status, "weknora_import_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{
		"project": project,
		"summary": summary,
	}})
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any) error {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return errors.New("Content-Type 必须为 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestSize)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("解析 JSON 失败：%w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDatasetNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "数据集不存在", nil)
	case strings.Contains(err.Error(), "数据集 ID"):
		writeAPIError(w, http.StatusBadRequest, "invalid_dataset_id", err.Error(), nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "storage_failed", err.Error(), nil)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, report *ValidationReport) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message, Report: report}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *studioServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store, max-age=0")
			w.Header().Set("Pragma", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *studioServer) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Scheme != "http" || !isLoopbackHost(parsed.Host) ||
				!strings.EqualFold(parsed.Host, r.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackAddress(addr string) bool {
	return isLoopbackHost(addr)
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if parsedHost, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsedHost
	} else if strings.HasPrefix(hostport, "[") && strings.HasSuffix(hostport, "]") {
		host = strings.Trim(hostport, "[]")
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
