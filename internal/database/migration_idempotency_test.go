package database_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var createTableLine = regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+`)

func TestVersionedUpMigrationsCreateTableIsIdempotent(t *testing.T) {
	root := filepath.Join("..", "..", "migrations", "versioned")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		path := filepath.Join(root, name)
		file, openErr := os.Open(path)
		require.NoError(t, openErr)
		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !createTableLine.MatchString(line) {
				continue
			}
			if strings.Contains(strings.ToUpper(line), "IF NOT EXISTS") {
				continue
			}
			offenders = append(offenders, name+":"+strconv.Itoa(lineNo))
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, file.Close())
	}
	require.Empty(t, offenders,
		"use CREATE TABLE IF NOT EXISTS in: %v", offenders)
}
