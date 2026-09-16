package handler

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/filetransport"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const maxBatchDownloadFiles = 200
const maxBatchDownloadBytes int64 = 512 * 1024 * 1024

// BatchDownloadKnowledgeRequest 指定同一知识库中需要下载的文档。
type BatchDownloadKnowledgeRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=200,dive,required,max=128"`
}

// BatchDownloadKnowledge godoc
// @Summary 批量下载知识文件
// @Description 将同一知识库的最多 200 个文档打包为 ZIP，原始内容合计不超过 512 MiB。任一文件失败时不返回残缺压缩包。
// @Tags 知识管理
// @Accept json
// @Produce application/zip
// @Param id path string true "知识库ID"
// @Param request body BatchDownloadKnowledgeRequest true "文档ID列表"
// @Success 200 {file} file "ZIP 压缩包"
// @Failure 400 {object} errors.AppError
// @Failure 403 {object} errors.AppError
// @Failure 404 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /knowledge-bases/{id}/knowledge/batch-download [post]
func (h *KnowledgeHandler) BatchDownloadKnowledge(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req BatchDownloadKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("请选择 1～200 个文档进行批量下载"))
		return
	}

	kbID := c.Param("id")
	grant, err := resolveHandlerKBAccessFor(
		c, kbID, h.kbService, h.kbShareService, h.agentShareService, types.OrgRoleEditor,
	)
	if err != nil {
		_ = c.Error(err)
		return
	}
	tenantID := grant.EffectiveTenantID
	ctx := types.WithExecutionTenant(c.Request.Context(), tenantID)
	ids := uniqueKnowledgeDownloadIDs(req.IDs)
	if len(ids) == 0 {
		_ = c.Error(errors.NewBadRequestError("文档 ID 不能为空"))
		return
	}

	items, err := h.kgService.GetKnowledgeBatch(ctx, tenantID, ids)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("无法获取待下载文档，请稍后重试"))
		return
	}
	byID := make(map[string]*types.Knowledge, len(items))
	for _, item := range items {
		if item != nil && item.KnowledgeBaseID == kbID && item.TenantID == tenantID {
			byID[item.ID] = item
		}
	}
	// 在读取任何文件之前检查整批文档，防止混入其他知识库或租户的 ID。
	for _, id := range ids {
		item := byID[id]
		if item == nil {
			_ = c.Error(errors.NewNotFoundError("部分文档不存在或不属于当前知识库，请刷新列表后重试"))
			return
		}
		if !item.IsManual() && item.FilePath == "" {
			_ = c.Error(errors.NewBadRequestError("所选文档没有可下载的原始文件，请取消选择后重试").WithDetails(
				gin.H{"knowledge_id": id},
			))
			return
		}
	}

	// 先在临时文件中完整生成压缩包，避免读取失败时向用户返回残缺 ZIP。
	archive, err := os.CreateTemp("", "weknora-download-*.zip")
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("无法创建下载压缩包，请稍后重试"))
		return
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	if err := writeKnowledgeDownloadArchive(
		ctx, archive, ids, h.kgService.GetKnowledgeFile, maxBatchDownloadBytes,
	); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(err)
		return
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		_ = c.Error(errors.NewInternalServerError("无法读取下载压缩包，请稍后重试"))
		return
	}
	filename := "knowledge-files-" + time.Now().Format("20060102-150405") + ".zip"
	if err := filetransport.Serve(c.Writer, c.Request, archive, filetransport.Options{
		Filename: filename, Download: true, ContentType: "application/zip", CacheControl: "private, no-store",
	}); err != nil {
		logger.Errorf(ctx, "Failed to send knowledge archive: %v", err)
	}
}

func uniqueKnowledgeDownloadIDs(input []string) []string {
	ids := make([]string, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, id := range input {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

type knowledgeDownloadOpener func(context.Context, string) (io.ReadCloser, string, error)

func writeKnowledgeDownloadArchive(
	ctx context.Context,
	output io.Writer,
	ids []string,
	open knowledgeDownloadOpener,
	limit int64,
) error {
	writer := zip.NewWriter(output)
	usedNames := make(map[string]bool, len(ids))
	var total int64
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, filename, err := open(ctx, id)
		if err != nil {
			return errors.NewInternalServerError("部分文件无法读取，未生成压缩包，请检查文档后重试").WithDetails(
				gin.H{"knowledge_id": id},
			)
		}
		name := uniqueKnowledgeDownloadName(filename, usedNames)
		// 原样存储可避免 PDF、Office 等已压缩文件再次压缩的 CPU 开销。
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			_ = file.Close()
			return errors.NewInternalServerError("无法写入下载压缩包，请稍后重试")
		}
		n, copyErr := io.Copy(entry, io.LimitReader(
			&knowledgeDownloadReader{ctx: ctx, reader: file}, limit-total+1,
		))
		closeErr := file.Close()
		total += n
		if total > limit {
			return errors.NewBadRequestError("所选文件合计超过 512 MiB，请分批下载")
		}
		if copyErr != nil || closeErr != nil {
			return errors.NewInternalServerError("部分文件读取失败，未生成压缩包，请稍后重试").WithDetails(
				gin.H{"knowledge_id": id},
			)
		}
	}
	if err := writer.Close(); err != nil {
		return errors.NewInternalServerError("无法完成下载压缩包，请稍后重试")
	}
	return nil
}

// 每次读取前检查取消信号，用户离开页面后停止继续打包。
type knowledgeDownloadReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *knowledgeDownloadReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func uniqueKnowledgeDownloadName(filename string, used map[string]bool) string {
	// 移除路径、控制字符及 Windows 非法字符，确保解压不会逃逸目录。
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	filename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, filename)
	filename = strings.Trim(filename, " .")
	if filename == "" {
		filename = "document"
	}
	ext := path.Ext(filename)
	if len(ext) > 20 {
		ext = ""
	}
	stem := strings.TrimSuffix(filename, ext)
	var shortened strings.Builder
	for _, r := range stem {
		if shortened.Len()+len(string(r)) > 180 {
			break
		}
		shortened.WriteRune(r)
	}
	stem = strings.TrimRight(shortened.String(), " .")
	if stem == "" {
		stem = "document"
	}
	reserved := strings.ToUpper(strings.Split(stem, ".")[0])
	reservedRunes := []rune(reserved)
	if reserved == "CON" || reserved == "PRN" || reserved == "AUX" || reserved == "NUL" ||
		(len(reservedRunes) == 4 && (strings.HasPrefix(reserved, "COM") || strings.HasPrefix(reserved, "LPT")) &&
			strings.ContainsRune("123456789¹²³", reservedRunes[3])) {
		stem = "_" + stem
	}
	name := stem + ext
	for index := 2; used[strings.ToLower(name)]; index++ {
		name = fmt.Sprintf("%s (%d)%s", stem, index, ext)
	}
	used[strings.ToLower(name)] = true
	return name
}
