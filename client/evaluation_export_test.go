package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExportEvaluationStreamsWithoutOrdinaryTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("format") != "json" {
			t.Errorf("format = %s", request.URL.Query().Get("format"))
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = response.Write([]byte(`{"schema_version":1}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	client.httpClient.Timeout = time.Millisecond
	var destination bytes.Buffer
	if err := client.ExportEvaluation(context.Background(), "task-a", "json", &destination); err != nil {
		t.Fatalf("ExportEvaluation() error = %v", err)
	}
	if destination.String() != `{"schema_version":1}` {
		t.Fatalf("export = %s", destination.String())
	}
}

func TestExportEvaluationReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusConflict)
		_, _ = response.Write([]byte(`{"code":1005,"message":"active"}`))
	}))
	defer server.Close()
	err := NewClient(server.URL).ExportEvaluation(context.Background(), "task-a", "csv", &bytes.Buffer{})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusConflict {
		t.Fatalf("error = %#v", err)
	}
}

func TestExportEvaluationValidatesInputs(t *testing.T) {
	client := NewClient("http://example.test")
	if err := client.ExportEvaluation(context.Background(), "", "json", &bytes.Buffer{}); err == nil {
		t.Fatal("empty task ID was accepted")
	}
	if err := client.ExportEvaluation(context.Background(), "task-a", "xlsx", &bytes.Buffer{}); err == nil {
		t.Fatal("unsupported format was accepted")
	}
	if err := client.ExportEvaluation(context.Background(), "task-a", "json", nil); err == nil {
		t.Fatal("nil destination was accepted")
	}
}
