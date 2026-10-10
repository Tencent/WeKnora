package service

import (
	"context"
	"strings"
	"testing"
)

func TestDatasetServiceRejectsUnknownDatasetID(t *testing.T) {
	svc := &DatasetService{}

	_, err := svc.GetDatasetByID(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("GetDatasetByID() error = nil, want unsupported dataset error")
	}
	if !strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("GetDatasetByID() error = %q, want unsupported dataset error", err)
	}
}

func TestLoadDatasetFromDirReturnsErrorForMissingFiles(t *testing.T) {
	_, err := loadDatasetFromDir(t.TempDir())
	if err == nil {
		t.Fatal("loadDatasetFromDir() error = nil, want missing parquet error")
	}
	if !strings.Contains(err.Error(), "load queries") {
		t.Fatalf("loadDatasetFromDir() error = %q, want queries context", err)
	}
}
