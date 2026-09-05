package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactVersionsPreserveBothFiles(t *testing.T) {
	old := MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "比赛信息.pptx", SourcePath: "/workspace/output/比赛信息.pptx"}
	next := old
	next.URL = "resource://4N1nAo-FZZoDEExDQz2yoA"
	body := "新版已生成。![PPT](" + old.URL + ")"
	got := ClarifyArtifactVersions(body, MessageArtifacts{next}, MessageArtifacts{old}, "zh-CN")
	require.True(t, strings.HasPrefix(got, body), "must not silently redirect an intentional historical reference")
	require.Contains(t, got, "正文引用的历史版本")
	require.Contains(t, got, "本轮生成的文件: ![比赛信息.pptx]("+next.URL+")")
	require.Equal(t, got, ClarifyArtifactVersions(got, MessageArtifacts{next}, MessageArtifacts{old}, "zh-CN"))
	require.Equal(t, body, ClarifyArtifactVersions(body, MessageArtifacts{old}, MessageArtifacts{old}, "zh-CN"))
	// An explicit comparison already contains the new file; leave it alone.
	comparison := body + "\n![新版](" + next.URL + ")"
	require.Equal(t, comparison, ClarifyArtifactVersions(comparison, MessageArtifacts{next}, MessageArtifacts{old}, "zh-CN"))
	next.SourcePath = "/workspace/output/other/比赛信息.pptx"
	require.Equal(t, body, ClarifyArtifactVersions(body, MessageArtifacts{next}, MessageArtifacts{old}, "zh-CN"), "matching file names alone cannot establish a version relationship")
}

func TestArtifactVersionsEscapeMarkdownDelimitersInFileName(t *testing.T) {
	old := MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "报告(终稿).pptx", SourcePath: "/workspace/output/报告(终稿).pptx"}
	next := old
	next.URL = "resource://4N1nAo-FZZoDEExDQz2yoA"
	got := ClarifyArtifactVersions("![旧]("+old.URL+")", MessageArtifacts{next}, MessageArtifacts{old}, "zh-CN")
	require.Contains(t, got, "![报告\\(终稿\\).pptx]("+old.URL+")")
	require.Contains(t, got, "![报告\\(终稿\\).pptx]("+next.URL+")")
	require.True(t, strings.HasSuffix(got, "]("+next.URL+")"))
}
