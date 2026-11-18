package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	fileService "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestParentReadsOnlyDeliveredArtifactsWithOriginalStorage(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&collaboration.Record{}, &types.MessageArtifactRecord{}))
	require.NoError(t, db.Exec(`CREATE TABLE sessions (
 id TEXT PRIMARY KEY, tenant_id INTEGER, user_id TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE messages (
 id TEXT PRIMARY KEY, session_id TEXT, role TEXT, deleted_at DATETIME)`).Error)
	scope := collaboration.Scope{ID: "parent-answer", SessionID: "parent", TenantID: 7, UserID: "alice"}
	r := collaboration.New(collaboration.NewStore(db), nil)
	b, err := r.AcquireParent(t.Context(), scope)
	require.NoError(t, err)
	memory := &webPageMemoryFiles{objects: map[string]string{}}
	fs := fileService.NewResourceCatalogFileService(memory, catalog)
	refs := map[string]string{}
	for _, name := range []string{"parent", "child", "unrelated", "foreign", "deleted"} {
		user := "alice"
		if name == "foreign" {
			user = "bob"
		}
		require.NoError(t, db.Exec("INSERT INTO sessions(id,tenant_id,user_id) VALUES(?,7,?)", name, user).Error)
		require.NoError(t, db.Exec("INSERT INTO messages(id,session_id,role) VALUES(?,?,'assistant')",
			name+"-answer", name).Error)
		ref, err := fs.SaveBytes(t.Context(), []byte("# Chapter\n"+name+" text\nlast line"), 7, name+".md", false)
		require.NoError(t, err)
		refs[name] = ref
		records := types.NewMessageArtifactRecords(name, name+"-answer", types.MessageArtifacts{{
			URL: ref, FileName: "report.md", FileType: ".md",
		}})
		require.NoError(t, db.Create(&records).Error)
	}
	require.NoError(t, db.Exec("UPDATE message_artifacts SET deleted_at = CURRENT_TIMESTAMP "+
		"WHERE session_id = 'deleted'").Error)
	require.NoError(t, r.Store.Change(b.Context(t.Context()), scope, func(g *collaboration.Group) error {
		for _, name := range []string{"child", "foreign", "deleted"} {
			g.Members = append(g.Members, &collaboration.Member{
				ID: name, Name: name, SessionID: name,
				Runs: []*collaboration.Run{{
					ID: name + "-run", State: "succeeded", Result: collaboration.Result{MessageID: name + "-answer"},
				}},
			})
		}
		return nil
	}))
	require.NoError(t, db.Model(&types.StoredResource{}).
		Where("handle = ?", strings.TrimPrefix(refs["child"], "resource://")).
		Update("storage_backend_id", "original").Error)
	var lastBackend string
	source := &agentArtifactFiles{
		db: db, catalog: catalog, binding: b, sessionID: "parent",
		files: func(_ context.Context, backend string) (interfaces.FileService, error) {
			lastBackend = backend
			return fs, nil
		},
	}
	tool := tools.NewReadFileTool(nil).WithArtifacts(source)
	read := func(path string) *types.ToolResult {
		result, err := tool.Execute(t.Context(), []byte(`{"path":"`+path+`","offset":2,"limit":1}`))
		require.NoError(t, err)
		return result
	}
	got := read(refs["child"])
	require.True(t, got.Success, got.Error)
	require.Contains(t, got.Output, "child text")
	require.Equal(t, "original", lastBackend)
	require.Equal(t, 3, got.Data["next_offset"])
	require.True(t, read(refs["parent"]).Success)
	reads := memory.reads
	for _, ref := range []string{refs["unrelated"], refs["foreign"], refs["deleted"], "sandbox:../report.md"} {
		require.False(t, read(ref).Success, ref)
	}
	require.Equal(t, reads, memory.reads, "unauthorized references must not reach file storage")
	ambiguous := read("sandbox:report.md")
	require.False(t, ambiguous.Success)
	require.Contains(t, ambiguous.Error, "multiple generated files")
	require.NoError(t, db.Exec("UPDATE message_artifacts SET file_name='chapter.md' WHERE session_id='child'").Error)
	require.True(t, read("sandbox:chapter.md").Success)
	// A recovered engine binds a new reader, using persisted rows rather than a byte cache.
	later := *source
	content, err := later.Read(t.Context(), refs["child"])
	require.NoError(t, err)
	require.Contains(t, string(content), "child text")
	require.NoError(t, r.Cancel(t.Context(), scope))
	_, err = later.Read(t.Context(), refs["child"])
	require.Error(t, err, "cancelled parents cannot retain read capability")
}
