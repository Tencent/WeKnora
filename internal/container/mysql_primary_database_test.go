package container

import (
	"strings"
	"testing"
)

func TestInitRetrieveEngineRegistryRejectsMySQLRetriever(t *testing.T) {
	t.Setenv("RETRIEVE_DRIVER", "mysql")

	_, err := initRetrieveEngineRegistry(nil, nil, nil)
	if err == nil {
		t.Fatal("RETRIEVE_DRIVER=mysql must be rejected")
	}
	if !strings.Contains(err.Error(), "business database only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitRetrieveEngineRegistryRequiresExternalRetrieverForMySQL(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("RETRIEVE_DRIVER", "")

	_, err := initRetrieveEngineRegistry(nil, nil, nil)
	if err == nil {
		t.Fatal("DB_DRIVER=mysql without RETRIEVE_DRIVER must be rejected")
	}
	if !strings.Contains(err.Error(), "external professional vector database") {
		t.Fatalf("unexpected error: %v", err)
	}
}
