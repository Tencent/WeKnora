package im

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/filetransport"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const originalTTL = time.Hour

type originalChannelKey struct{}

type originalChannel struct {
	id, agentID string
}

type originalGrant struct {
	TenantID                                      uint64
	ChannelID, Revision, KnowledgeID, FileVersion string
	ChannelVersion, AgentVersion                  int64
}
type originalFile struct {
	name     string
	physical string
	provider string
	backend  string
	version  string
}

func originalDownloadBase() string {
	u, err := url.Parse(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/im/citation-download"
	return u.String()
}

func originalGrantKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return "im:cited-original:" + hex.EncodeToString(h[:])
}

func citedOriginals(answer string, refs []*types.SearchResult) []*types.SearchResult {
	byChunk := make(map[string]*types.SearchResult, len(refs))
	for _, ref := range refs {
		if ref != nil && ref.ID != "" {
			byChunk[ref.ID] = ref
		}
	}
	seen := map[string]bool{}
	var result []*types.SearchResult
	for _, tag := range imCitationTagRe.FindAllString(answer, -1) {
		elem, err := xml.NewDecoder(strings.NewReader(tag)).Token()
		start, ok := elem.(xml.StartElement)
		if err != nil || !ok || start.Name.Local != "kb" {
			continue
		}
		var chunkID, kbID string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "chunk_id":
				chunkID = attr.Value
			case "kb_id":
				kbID = attr.Value
			}
		}
		ref := byChunk[chunkID]
		if ref == nil || ref.KnowledgeID == "" || (kbID != "" && kbID != ref.KnowledgeBaseID) || seen[ref.KnowledgeID] {
			continue
		}
		seen[ref.KnowledgeID] = true
		result = append(result, ref)
	}
	return result
}

func (s *Service) originalScope(
	ctx context.Context, tenantID uint64, channelID string,
) (*IMChannel, *originalSharing, *types.CustomAgent, error) {
	var ch IMChannel
	err := s.db.WithContext(ctx).Select("id", "tenant_id", "agent_id", "updated_at").
		Where("tenant_id = ? AND id = ? AND enabled = ? AND platform = ?", tenantID, channelID, true, PlatformFeishu).
		Take(&ch).Error
	if err != nil {
		return nil, nil, nil, err
	}
	var scope originalSharing
	err = s.db.WithContext(ctx).Where("channel_id = ? AND agent_id = ?", ch.ID, ch.AgentID).Take(&scope).Error
	if err != nil {
		return nil, nil, nil, err
	}
	agent, err := s.authorizeOriginalSharing(ctx, &ch, &scope)
	return &ch, &scope, agent, err
}

func (s *Service) resolveCitedOriginal(
	ctx context.Context, tenantID uint64, kbID, docID string,
) (*originalFile, error) {
	k, err := s.knowledgeService.GetKnowledgeByID(ctx, docID)
	if err != nil || k == nil || k.ID != docID || k.TenantID != tenantID || k.KnowledgeBaseID != kbID {
		return nil, access.ErrForbidden
	}
	if k.IsManual() || k.DeletedAt.Valid || k.EnableStatus == "disabled" ||
		k.ParseStatus == types.ParseStatusDeleting || access.RejectMovingKnowledge(k) != nil {
		return nil, access.ErrForbidden
	}
	// Use the registered storage instance. Guessing a backend from a legacy
	// filename could open a different object after a storage configuration change.
	if _, ok := types.ParseResourcePath(k.FilePath); !ok || s.resourceCatalog == nil || s.storageResolver == nil {
		return nil, access.ErrForbidden
	}
	r, err := s.resourceCatalog.Resolve(ctx, k.FilePath)
	if err != nil || r == nil || r.TenantID != tenantID || r.DeletedAt.Valid || r.State != types.ResourceStateActive {
		return nil, access.ErrForbidden
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(time.Now()) {
		return nil, access.ErrForbidden
	}
	if r.StorageBackendID == "" || r.Provider == "" ||
		secutils.ValidateStoragePathTenant(r.PhysicalPath, tenantID) != nil {
		return nil, access.ErrForbidden
	}
	var sourceBindings int64
	err = s.db.WithContext(ctx).Model(&types.ResourceBinding{}).
		Where("tenant_id = ? AND resource_id = ? AND owner_type = ? AND owner_id = ? AND relation = ?",
			tenantID, r.ID, types.ResourceOwnerKnowledge, k.ID, types.ResourceRelationSourceFile).
		Count(&sourceBindings).Error
	if err != nil || sourceBindings == 0 {
		return nil, access.ErrForbidden
	}
	identity := []string{
		k.FilePath, k.FileName, k.FileHash, fmt.Sprint(k.FileSize),
		r.ID, r.PhysicalPath, r.StorageBackendID, r.Provider, r.ContentHash,
	}
	version := sha256.Sum256([]byte(strings.Join(identity, "\x00")))
	return &originalFile{
		name: k.FileName, physical: r.PhysicalPath, provider: r.Provider,
		backend: r.StorageBackendID, version: hex.EncodeToString(version[:]),
	}, nil
}

func (s *Service) originalDownloadLinks(ctx context.Context, answer string, refs []*types.SearchResult) string {
	channel, _ := ctx.Value(originalChannelKey{}).(originalChannel)
	base := originalDownloadBase()
	if channel.id == "" || base == "" || s.redis == nil || len(refs) == 0 {
		return ""
	}
	cited := citedOriginals(answer, refs)
	if len(cited) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tenantID, _ := types.TenantIDFromContext(ctx)
	ch, scope, agent, err := s.originalScope(ctx, tenantID, channel.id)
	if err != nil || ch.AgentID != channel.agentID {
		return ""
	}
	var links strings.Builder
	for _, ref := range cited {
		if ref.KnowledgeBaseID != scope.KBID {
			continue
		}
		f, err := s.resolveCitedOriginal(ctx, tenantID, scope.KBID, ref.KnowledgeID)
		if err != nil {
			continue
		}
		var buf [32]byte
		if _, err := rand.Read(buf[:]); err != nil {
			continue
		}
		token := base64.RawURLEncoding.EncodeToString(buf[:])
		grant := originalGrant{
			TenantID: tenantID, ChannelID: ch.ID, Revision: scope.Revision,
			KnowledgeID: ref.KnowledgeID, FileVersion: f.version,
			ChannelVersion: ch.UpdatedAt.UnixNano(), AgentVersion: agent.UpdatedAt.UnixNano(),
		}
		b, err := json.Marshal(grant)
		if err != nil || s.redis.Set(ctx, originalGrantKey(token), b, originalTTL).Err() != nil {
			continue
		}
		name := strings.Join(strings.Fields(f.name), " ")
		name = strings.NewReplacer(
			"[", "［", "]", "］", "<", "＜", ">", "＞", "\\", "＼", "*", "＊",
			"`", "｀", "|", "｜", "!", "！",
		).Replace(name)
		fmt.Fprintf(&links, "\n- [%s](%s#%s)", name, base, token)
	}
	if links.Len() == 0 {
		return ""
	}
	label := "Original files (links expire in one hour)"
	if lang, _ := types.LanguageFromContext(ctx); strings.HasPrefix(lang, "zh") {
		label = "引用原文件（链接一小时内有效）"
	}
	return "\n\n" + label + links.String()
}

// The URL fragment stays in the recipient's browser. A deliberate download
// submits the token in the body, away from proxy access logs and link previews.
const originalLandingScript = `
const form = document.forms[0];
form.elements.token.value = location.hash.slice(1);
history.replaceState(null, "", location.pathname);
form.querySelector("button").disabled = !form.elements.token.value;
`

const originalLanding = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Download original</title>
<form method="post">
  <input type="hidden" name="token">
  <button disabled>Download original / 下载原文件</button>
</form>
<script>` + originalLandingScript + `</script>`

// ServeOriginalDownload presents the confirmation page and streams the
// registered object after rechecking the channel and its live delegation.
func (s *Service) ServeOriginalDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet {
		scriptHash := sha256.Sum256([]byte(originalLandingScript))
		scriptSource := "'sha256-" + base64.StdEncoding.EncodeToString(scriptHash[:]) + "'"
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src "+scriptSource+
			"; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(originalLanding))
		return
	}
	deny := func() { http.NotFound(w, r) }
	r.Body = http.MaxBytesReader(w, r.Body, 512)
	if r.Method != http.MethodPost || r.ParseForm() != nil || s.redis == nil {
		deny()
		return
	}
	token := r.PostForm.Get("token")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		deny()
		return
	}
	ctx := r.Context()
	b, err := s.redis.Get(ctx, originalGrantKey(token)).Bytes()
	var g originalGrant
	if err != nil || json.Unmarshal(b, &g) != nil {
		deny()
		return
	}
	ctx = types.WithExecutionTenant(ctx, g.TenantID)
	tenant, err := s.tenantService.GetTenantByID(ctx, g.TenantID)
	if err != nil || tenant == nil || tenant.ID != g.TenantID {
		deny()
		return
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ch, scope, agent, err := s.originalScope(ctx, g.TenantID, g.ChannelID)
	if err != nil || scope.Revision != g.Revision || ch.UpdatedAt.UnixNano() != g.ChannelVersion ||
		agent.UpdatedAt.UnixNano() != g.AgentVersion {
		deny()
		return
	}
	f, err := s.resolveCitedOriginal(ctx, g.TenantID, scope.KBID, g.KnowledgeID)
	if err != nil || f.version != g.FileVersion {
		deny()
		return
	}
	backend, _, err := s.storageResolver.ResolveFileService(
		ctx, tenant, f.backend, f.provider, storageurl.LocalStorageBaseDir(),
	)
	if err != nil || backend == nil {
		deny()
		return
	}
	reader, err := backend.GetFile(ctx, f.physical)
	if err != nil {
		deny()
		return
	}
	if err := filetransport.Serve(w, r, reader, filetransport.Options{
		Filename: f.name, Download: true, ContentType: "application/octet-stream", CacheControl: "private, no-store",
	}); err != nil {
		logger.Warn(ctx, "[IM] Original file transfer interrupted")
	}
}
