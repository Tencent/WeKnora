package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/xuri/excelize/v2"
)

func writeAgentCollectionCSV(
	dir, exportID string,
	profiles []*types.AgentCollectionProfile,
	fields []string,
) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, exportID+".csv")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return "", err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(agentCollectionExportHeader(fields)); err != nil {
		return "", err
	}
	for _, profile := range profiles {
		if err := writer.Write(agentCollectionExportRow(profile, fields)); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	remove = false
	return path, nil
}

func writeAgentCollectionXLSX(
	dir, exportID string,
	profiles []*types.AgentCollectionProfile,
	fields []string,
) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, exportID+".xlsx")
	book := excelize.NewFile()
	defer book.Close()
	book.SetSheetName("Sheet1", "Profiles")
	rows := [][]string{agentCollectionExportHeader(fields)}
	for _, profile := range profiles {
		rows = append(rows, agentCollectionExportRow(profile, fields))
	}
	for rowIndex, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, rowIndex+1)
		values := make([]interface{}, len(row))
		for index := range row {
			values[index] = row[index]
		}
		if err := book.SetSheetRow("Profiles", cell, &values); err != nil {
			return "", err
		}
	}
	if err := book.SaveAs(path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func agentCollectionExportHeader(fields []string) []string {
	return append([]string{"tenant_id", "agent_tenant_id", "agent_id", "user_id", "is_complete", "updated_at"}, fields...)
}

func agentCollectionExportRow(profile *types.AgentCollectionProfile, fields []string) []string {
	row := []string{
		strconv.FormatUint(profile.TenantID, 10), strconv.FormatUint(profile.AgentTenantID, 10),
		profile.AgentID, profile.UserID, strconv.FormatBool(profile.IsComplete), profile.UpdatedAt.UTC().Format(time.RFC3339),
	}
	for _, key := range fields {
		row = append(row, formatAgentCollectionValue(profile.Values[key]))
	}
	return row
}

func formatAgentCollectionValue(raw any) string {
	if entry, ok := raw.(map[string]any); ok {
		raw = entry["value"]
	} else if data, err := json.Marshal(raw); err == nil {
		var entry map[string]any
		if json.Unmarshal(data, &entry) == nil && entry["value"] != nil {
			raw = entry["value"]
		}
	}
	switch value := raw.(type) {
	case nil:
		return ""
	case []string:
		return strings.Join(value, "; ")
	case []any:
		parts := make([]string, len(value))
		for index := range value {
			parts[index] = fmt.Sprint(value[index])
		}
		return strings.Join(parts, "; ")
	case string:
		return safeSpreadsheetText(value)
	default:
		return fmt.Sprint(value)
	}
}

func safeSpreadsheetText(value string) string {
	trimmed := strings.TrimLeft(value, " \r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t':
		return "'" + value
	default:
		return value
	}
}
