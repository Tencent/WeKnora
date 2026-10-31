package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// acmePluginsArchive builds the acme-plugins marketplace used by plugin tests.
// The bytes are fixed so two imports of the same archive share one row.
func acmePluginsArchive(t *testing.T) []byte {
	t.Helper()
	files := []struct {
		name string
		body string
	}{
		{
			name: "acme-plugins/.weknora-plugin/marketplace.json",
			body: `{
  "name": "acme-plugins",
  "owner": { "name": "Acme", "email": "plugins@acme.dev" },
  "metadata": {
    "description": "Acme curated plugins for databases, scheduling and SQL",
    "version": "1.0.0"
  },
  "plugins": [
    {
      "name": "neon",
      "source": "./plugins/neon",
      "description": "Neon Postgres skills and MCP",
      "category": "database",
      "keywords": ["postgres", "serverless"]
    },
    {
      "name": "calcom",
      "source": "./plugins/calcom",
      "description": "Cal.com scheduling MCP",
      "category": "productivity",
      "keywords": ["calendar", "meeting"]
    },
    {
      "name": "sql-style",
      "source": "./plugins/sql-style",
      "description": "SQL style guide",
      "category": "development",
      "keywords": ["sql", "lint"]
    }
  ]
}
`,
		},
		{
			name: "acme-plugins/plugins/calcom/.mcp.json",
			body: `{
  "mcpServers": {
    "calcom": { "type": "http", "url": "https://mcp.cal.com/mcp" }
  }
}
`,
		},
		{
			name: "acme-plugins/plugins/calcom/.weknora-plugin/plugin.json",
			body: `{
  "name": "calcom",
  "version": "0.1.0",
  "description": "Book and manage Cal.com meetings"
}
`,
		},
		{
			name: "acme-plugins/plugins/neon/.mcp.json",
			body: `{
  "mcpServers": {
    "neon": {
      "type": "http",
      "url": "https://mcp.neon.tech/mcp"
    },
    "context7": {
      "type": "http",
      "url": "https://mcp.context7.com/mcp"
    }
  }
}
`,
		},
		{
			name: "acme-plugins/plugins/neon/.weknora-plugin/plugin.json",
			body: `{
  "name": "neon",
  "version": "1.0.0",
  "description": "Manage Neon Serverless Postgres projects and branches",
  "author": { "name": "Neon", "url": "https://neon.com" },
  "license": "Apache-2.0",
  "keywords": ["postgres", "database"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json"
}
`,
		},
		{
			name: "acme-plugins/plugins/neon/skills/neon-branches/SKILL.md",
			body: `---
name: neon-branches
description: Create and manage Neon database branches.
---

# Neon branches

1. List branches with the ` + "`neon`" + ` MCP server before creating new ones.
2. Name branches after the git branch they back.
`,
		},
		{
			name: "acme-plugins/plugins/neon/skills/neon-postgres/SKILL.md",
			body: `---
name: neon-postgres
description: Work with Neon Serverless Postgres projects and databases.
---

# Neon Postgres

1. Use the ` + "`neon`" + ` MCP server to list projects before running queries.
2. Look up library documentation with the ` + "`context7`" + ` MCP server.
3. Prefer pooled connection strings for serverless workloads.
`,
		},
		{
			name: "acme-plugins/plugins/sql-style/skills/sql-style/SKILL.md",
			body: `---
name: sql-style
description: SQL style guide for writing and reviewing queries. Use when the user writes, formats, or reviews SQL.
---

# SQL style

1. Uppercase keywords, lowercase identifiers in snake_case.
2. One clause per line: ` + "`SELECT`, `FROM`, `WHERE`, `GROUP BY`, `ORDER BY`" + `.
3. Always list columns explicitly instead of ` + "`SELECT *`" + `.
`,
		},
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	require.NoError(t, err)
	gz.Name = "acme-plugins.tar"
	gz.ModTime = time.Unix(0, 0).UTC()
	gz.OS = 255
	tw := tar.NewWriter(gz)
	modTime := time.Unix(0, 0).UTC()
	for _, file := range files {
		body := []byte(file.body)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     file.name,
			Mode:     0o644,
			Size:     int64(len(body)),
			ModTime:  modTime,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}))
		_, err = tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}
