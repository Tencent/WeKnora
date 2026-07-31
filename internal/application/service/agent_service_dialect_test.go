package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestFilterDatabaseCompatibleToolsDropsDatabaseQueryForMySQL(t *testing.T) {
	allowed := []string{tools.ToolThinking, tools.ToolDatabaseQuery, tools.ToolKnowledgeSearch}

	got, dropped := filterDatabaseCompatibleTools(allowed, "mysql")

	require.Equal(t, []string{tools.ToolThinking, tools.ToolKnowledgeSearch}, got)
	require.Equal(t, []string{tools.ToolDatabaseQuery}, dropped)
}

func TestFilterDatabaseCompatibleToolsKeepsDatabaseQueryForPostgres(t *testing.T) {
	allowed := []string{tools.ToolThinking, tools.ToolDatabaseQuery}

	got, dropped := filterDatabaseCompatibleTools(allowed, "postgres")

	require.Equal(t, allowed, got)
	require.Empty(t, dropped)
}

func TestFilterDatabaseCompatibleToolsKeepsDatabaseQueryForSQLite(t *testing.T) {
	allowed := []string{tools.ToolThinking, tools.ToolDatabaseQuery}

	got, dropped := filterDatabaseCompatibleTools(allowed, "sqlite")

	require.Equal(t, allowed, got)
	require.Empty(t, dropped)
}
