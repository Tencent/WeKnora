package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// UpsertFAQEntries imports or appends FAQ entries asynchronously.
// Returns task ID (UUID) for tracking import progress.
func (s *knowledgeService) UpsertFAQEntries(ctx context.Context,
	kbID string, payload *types.FAQBatchUpsertPayload,
) (string, error) {
	if payload == nil || len(payload.Entries) == 0 {
		return "", werrors.NewBadRequestError("FAQ 条目不能为空")
	}
	if payload.Mode == "" {
		payload.Mode = types.FAQBatchModeAppend
	}
	if payload.Mode != types.FAQBatchModeAppend && payload.Mode != types.FAQBatchModeReplace {
		return "", werrors.NewBadRequestError("模式仅支持 append 或 replace")
	}

	// 验证知识库是否存在且有效
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return "", err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 使用传入的TaskID，如果没传则生成增强的TaskID
	taskID := payload.TaskID
	if taskID == "" {
		taskID = secutils.GenerateTaskID("faq_import", tenantID, kbID)
	}

	var knowledgeID string

	// 检查是否有正在进行的导入任务（通过Redis）
	runningTaskID, err := s.getRunningFAQImportTaskID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to check running import task: %v", err)
		// 检查失败不影响导入，继续执行
	} else if runningTaskID != "" {
		logger.Warnf(ctx, "Import task already running for KB %s: %s", kbID, runningTaskID)
		return "", werrors.NewBadRequestError(fmt.Sprintf("该知识库已有导入任务正在进行中（任务ID: %s），请等待完成后再试", runningTaskID))
	}

	// 确保 FAQ knowledge 存在
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return "", fmt.Errorf("failed to ensure FAQ knowledge: %w", err)
	}
	knowledgeID = faqKnowledge.ID

	// 记录任务入队时间
	enqueuedAt := time.Now().Unix()
	instanceID := uuid.NewString()
	runningInfoSet := false
	enqueueSucceeded := false

	// 设置 KB 的运行中任务信息
	if err := s.setRunningFAQImportInfo(ctx, kbID, &runningFAQImportInfo{
		TaskID:     taskID,
		EnqueuedAt: enqueuedAt,
		InstanceID: instanceID,
	}); err != nil {
		logger.Errorf(ctx, "Failed to set running FAQ import task info: %v", err)
		// 不影响任务执行，继续
	} else {
		runningInfoSet = true
	}
	defer func() {
		if !runningInfoSet || enqueueSucceeded {
			return
		}
		if clearErr := s.clearRunningFAQImportInfoIfMatches(ctx, kbID, taskID, instanceID, enqueuedAt); clearErr != nil {
			logger.Warnf(ctx, "Failed to clear FAQ import running info after setup failure: %v", clearErr)
		}
	}()

	// 初始化导入任务状态到Redis
	progress := &types.FAQImportProgress{
		TaskID:        taskID,
		KBID:          kbID,
		KnowledgeID:   knowledgeID,
		Status:        types.FAQImportStatusPending,
		Progress:      0,
		Total:         len(payload.Entries),
		Processed:     0,
		SuccessCount:  0,
		FailedCount:   0,
		FailedEntries: make([]types.FAQFailedEntry, 0),
		Message:       "任务已创建，等待处理",
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
		DryRun:        payload.DryRun,
	}
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to initialize FAQ import task status: %v", err)
		return "", fmt.Errorf("failed to initialize task: %w", err)
	}

	logger.Infof(ctx, "FAQ import task initialized: %s, kb_id: %s, total entries: %d, dry_run: %v",
		taskID, kbID, len(payload.Entries), payload.DryRun)

	// Enqueue FAQ import task to Asynq
	logger.Info(ctx, "Enqueuing FAQ import task to Asynq")

	// 构建任务 payload
	taskPayload := types.FAQImportPayload{
		TenantID:    tenantID,
		TaskID:      taskID,
		KBID:        kbID,
		KnowledgeID: knowledgeID,
		Mode:        payload.Mode,
		DryRun:      payload.DryRun,
		EnqueuedAt:  enqueuedAt,
		InstanceID:  instanceID,
		Initiator:   types.TaskInitiatorFromContext(ctx),
	}

	// 阈值：超过 200 条或序列化后超过 50KB 时使用对象存储
	const (
		entryCountThreshold  = 200
		payloadSizeThreshold = 50 * 1024 // 50KB
	)

	entryCount := len(payload.Entries)
	if entryCount > entryCountThreshold {
		// 数据量较大，上传到对象存储
		entriesData, err := json.Marshal(payload.Entries)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal FAQ entries: %v", err)
			return "", fmt.Errorf("failed to marshal entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries size: %d bytes, uploading to object storage", len(entriesData))

		// 上传到私有桶（主桶），任务处理完成后清理
		fileName := fmt.Sprintf("faq_import_entries_%s_%d.json", taskID, enqueuedAt)
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to: %s", entriesURL)
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount
	} else {
		// 数据量较小，直接存储在 payload 中
		taskPayload.Entries = payload.Entries
	}

	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal FAQ import task payload: %v", err)
		return "", fmt.Errorf("failed to marshal task payload: %w", err)
	}

	// 再次检查 payload 大小
	if len(payloadBytes) > payloadSizeThreshold && taskPayload.EntriesURL == "" {
		// payload 太大但还没上传，现在上传
		entriesData, _ := json.Marshal(payload.Entries)
		fileName := fmt.Sprintf("faq_import_entries_%s_%d.json", taskID, enqueuedAt)
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to (size exceeded): %s", entriesURL)
		taskPayload.Entries = nil
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount

		payloadBytes, _ = json.Marshal(taskPayload)
	}

	logger.Infof(ctx, "FAQ import task payload size: %d bytes", len(payloadBytes))

	maxRetry := 5
	if payload.DryRun {
		maxRetry = 3 // dry run 重试次数少一些
	}

	// 使用 taskID:instanceID 作为 asynq 的唯一任务标识
	asynqTaskID := fmt.Sprintf("%s:%s", taskID, instanceID)

	task := asynq.NewTask(
		types.TypeFAQImport,
		payloadBytes,
		asynq.TaskID(asynqTaskID),
		asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(2*time.Hour),
	)
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue FAQ import task: %v", err)
		return "", fmt.Errorf("failed to enqueue task: %w", err)
	}
	logger.Infof(ctx, "Enqueued FAQ import task: id=%s queue=%s task_id=%s dry_run=%v", info.ID, info.Queue, taskID, payload.DryRun)
	enqueueSucceeded = true

	if !payload.DryRun {
		recordKBActivity(ctx, s.audit, tenantID, kbID, types.AuditActionFAQImportStarted,
			"faq_entry", knowledgeID, types.AuditOutcomeAccepted,
			map[string]any{
				"task_id": taskID, "mode": payload.Mode, "total": len(payload.Entries),
				"trigger": kbActivityTrigger(ctx), "processing_status": "pending",
			})
	}

	return taskID, nil
}

// generateFailedEntriesCSV 生成失败条目的 CSV 文件并上传
func (s *knowledgeService) generateFailedEntriesCSV(ctx context.Context,
	tenantID uint64, taskID string, failedEntries []types.FAQFailedEntry,
) (string, error) {
	// 生成 CSV 内容
	var buf strings.Builder

	// 写入 BOM 以支持 Excel 正确识别 UTF-8
	buf.WriteString("\xEF\xBB\xBF")

	// 写入表头
	buf.WriteString("错误原因,分类(必填),问题(必填),相似问题(选填-多个用##分隔),反例问题(选填-多个用##分隔),机器人回答(必填-多个用##分隔),是否全部回复(选填-默认FALSE),是否停用(选填-默认FALSE)\n")

	// 写入数据行
	for _, entry := range failedEntries {
		// CSV 转义：如果内容包含逗号、引号或换行，需要用引号包裹并转义内部引号
		reason := csvEscape(entry.Reason)
		tagName := csvEscape(entry.TagName)
		standardQ := csvEscape(entry.StandardQuestion)
		similarQs := ""
		if len(entry.SimilarQuestions) > 0 {
			similarQs = csvEscape(strings.Join(entry.SimilarQuestions, "##"))
		}
		negativeQs := ""
		if len(entry.NegativeQuestions) > 0 {
			negativeQs = csvEscape(strings.Join(entry.NegativeQuestions, "##"))
		}
		answers := ""
		if len(entry.Answers) > 0 {
			answers = csvEscape(strings.Join(entry.Answers, "##"))
		}
		answerAll := "false"
		if entry.AnswerAll {
			answerAll = "true"
		}
		isDisabled := "false"
		if entry.IsDisabled {
			isDisabled = "true"
		}

		buf.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s\n",
			reason, tagName, standardQ, similarQs, negativeQs, answers, answerAll, isDisabled))
	}

	// 上传 CSV 文件到临时存储（会自动过期）
	fileName := fmt.Sprintf("faq_dryrun_failed_%s.csv", taskID)
	filePath, err := s.fileSvc.SaveBytes(ctx, []byte(buf.String()), tenantID, fileName, true)
	if err != nil {
		return "", fmt.Errorf("failed to save CSV file: %w", err)
	}

	// 获取下载 URL
	fileURL, err := s.fileSvc.GetFileURL(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get file URL: %w", err)
	}

	logger.Infof(ctx, "Generated failed entries CSV: %s, entries: %d", fileURL, len(failedEntries))
	return fileURL, nil
}

// csvEscape 转义 CSV 字段
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		// 将内部引号替换为两个引号，并用引号包裹整个字段
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

// saveFAQImportResultToDatabase 保存FAQ导入结果统计到数据库
func (s *knowledgeService) saveFAQImportResultToDatabase(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress, originalTotalEntries int,
) error {
	// 获取FAQ知识库实例
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("failed to get FAQ knowledge: %w", err)
	}

	// 计算跳过的条目数（总数 - 完全成功 - 部分失败 - 完全失败）
	skippedCount := originalTotalEntries - progress.SuccessCount - progress.PartialFailedCount - progress.FailedCount
	if skippedCount < 0 {
		skippedCount = 0
	}

	// 创建导入结果统计
	importResult := &types.FAQImportResult{
		TotalEntries:       originalTotalEntries,
		SuccessCount:       progress.SuccessCount,
		FailedCount:        progress.FailedCount,
		PartialFailedCount: progress.PartialFailedCount,
		SkippedCount:       skippedCount,
		MergedCount:        progress.MergedCount,
		AddedCount:         progress.AddedCount,
		ImportMode:         payload.Mode,
		ImportedAt:         time.Now(),
		TaskID:             payload.TaskID,
		ProcessingTime:     time.Now().Unix() - progress.CreatedAt, // 处理耗时（秒）
		DisplayStatus:      "open",                                 // 新导入的结果默认显示
	}

	// 如果有失败/部分失败条目 CSV 下载 URL，则写入结果（FailedEntries 包含完全失败与部分失败）
	if progress.FailedEntriesURL != "" {
		importResult.FailedEntriesURL = progress.FailedEntriesURL
	}

	// 设置导入结果到Knowledge的metadata中
	if err := knowledge.SetLastFAQImportResult(importResult); err != nil {
		return fmt.Errorf("failed to set FAQ import result: %w", err)
	}

	// 更新数据库
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return fmt.Errorf("failed to update knowledge with import result: %w", err)
	}

	logger.Infof(ctx, "Saved FAQ import result to database: knowledge_id=%s, task_id=%s, total=%d, success=%d, added=%d, merged=%d, failed=%d, partial_failed=%d, skipped=%d",
		payload.KnowledgeID, payload.TaskID, originalTotalEntries, progress.SuccessCount, progress.AddedCount, progress.MergedCount, progress.FailedCount, progress.PartialFailedCount, skippedCount)

	return nil
}

// buildFAQFailedEntry 构建 FAQFailedEntry
func buildFAQFailedEntry(idx int, reason string, entry *types.FAQEntryPayload) types.FAQFailedEntry {
	answerAll := false
	if entry.AnswerStrategy != nil && *entry.AnswerStrategy == types.AnswerStrategyAll {
		answerAll = true
	}
	isDisabled := false
	if entry.IsEnabled != nil && !*entry.IsEnabled {
		isDisabled = true
	}
	return types.FAQFailedEntry{
		Index:             idx,
		Reason:            reason,
		TagName:           entry.TagName,
		StandardQuestion:  strings.TrimSpace(entry.StandardQuestion),
		SimilarQuestions:  entry.SimilarQuestions,
		NegativeQuestions: entry.NegativeQuestions,
		Answers:           entry.Answers,
		AnswerAll:         answerAll,
		IsDisabled:        isDisabled,
	}
}

func buildFAQPartialFailedEntry(idx int, entry *types.FAQEntryPayload,
	removedSimilarQuestions, removedNegativeQuestions []string,
) types.FAQFailedEntry {
	answerAll := false
	if entry.AnswerStrategy != nil && *entry.AnswerStrategy == types.AnswerStrategyAll {
		answerAll = true
	}
	isDisabled := false
	if entry.IsEnabled != nil && !*entry.IsEnabled {
		isDisabled = true
	}

	// 构建失败原因描述：汇总信息 + 详细信息
	var summary []string
	if len(removedSimilarQuestions) > 0 {
		summary = append(summary, fmt.Sprintf("%d条相似问被移除", len(removedSimilarQuestions)))
	}
	if len(removedNegativeQuestions) > 0 {
		summary = append(summary, fmt.Sprintf("%d条反例被移除", len(removedNegativeQuestions)))
	}

	// 完整的 reason：汇总 | 相似问详情 | 反例详情
	var reasonParts []string
	reasonParts = append(reasonParts, "部分成功："+strings.Join(summary, "，"))
	if len(removedSimilarQuestions) > 0 {
		reasonParts = append(reasonParts, strings.Join(removedSimilarQuestions, "; "))
	}
	if len(removedNegativeQuestions) > 0 {
		reasonParts = append(reasonParts, strings.Join(removedNegativeQuestions, "; "))
	}

	return types.FAQFailedEntry{
		Index:                    idx,
		Reason:                   strings.Join(reasonParts, " | "),
		IsPartialFailure:         true,
		TagName:                  entry.TagName,
		StandardQuestion:         strings.TrimSpace(entry.StandardQuestion),
		SimilarQuestions:         entry.SimilarQuestions,
		NegativeQuestions:        entry.NegativeQuestions,
		Answers:                  entry.Answers,
		AnswerAll:                answerAll,
		IsDisabled:               isDisabled,
		RemovedSimilarQuestions:  removedSimilarQuestions,
		RemovedNegativeQuestions: removedNegativeQuestions,
	}
}

// executeFAQDryRunValidation 执行 FAQ dry run 验证，返回通过验证的条目索引
func (s *knowledgeService) executeFAQDryRunValidation(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress,
) []int {
	entries := payload.Entries

	// 用于记录已通过基本验证和重复检查的条目索引，后续进行安全检查
	validEntryIndices := make([]int, 0, len(entries))

	// 根据模式选择不同的验证逻辑
	if payload.Mode == types.FAQBatchModeAppend {
		validEntryIndices = s.validateEntriesForAppendModeWithProgress(ctx, payload.TenantID, payload.KBID, entries, progress)
	} else {
		validEntryIndices = s.validateEntriesForReplaceModeWithProgress(ctx, entries, progress)
	}

	return validEntryIndices
}

// validateEntriesForAppendModeWithProgress 验证 Append 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
// validateEntriesForAppendModeWithProgress 验证 Append 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
//
// 分四阶段校验：
// 阶段一（预校验）：
//  1. 标准问 - 仅文件内部去重；如已有KB标准问则标记为合并候选
//  2. 相似问 - 对比文件+知识库（合并候选排除自身已有问题） → 单条移除冲突相似问
//  3. 反例   - 仅对比当前QA自身标准问+相似问 → 单条移除冲突反例
//
// 阶段二（后校验，仅合并候选）：
//  4. 对合并后的完整数据重跑反例校验 → 冲突则整条回退到合并前状态
func (s *knowledgeService) validateEntriesForAppendModeWithProgress(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	totalEntries := len(entries)

	// 查询知识库中已有的所有 FAQ chunks 的 metadata
	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list existing FAQ chunks for dry run: %v", err)
	}

	// 构建已存在的标准问 → chunk 映射（用于合并候选识别）
	existingStdQToChunk := make(map[string]*types.Chunk)
	// 构建已存在的所有问题 → 所属 chunkID 映射（用于相似问冲突检测）
	existingQuestionToChunkID := make(map[string]string)
	// 构建每个 chunk 拥有的问题集合（用于合并时排除自身）
	existingChunkQuestions := make(map[string]map[string]bool)
	// 构建 chunkID → 标准问 映射（用于冲突失败原因展示）
	existingChunkIDToStdQ := make(map[string]string)

	for _, chunk := range existingChunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}
		qs := make(map[string]bool)
		if meta.StandardQuestion != "" {
			existingStdQToChunk[meta.StandardQuestion] = chunk
			existingQuestionToChunkID[meta.StandardQuestion] = chunk.ID
			qs[meta.StandardQuestion] = true
		}
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestionToChunkID[q] = chunk.ID
				qs[q] = true
			}
		}
		existingChunkQuestions[chunk.ID] = qs
		existingChunkIDToStdQ[chunk.ID] = meta.StandardQuestion
	}

	// 合并候选跟踪：entry index → 目标已有 chunk
	mergeChunkMap := make(map[int]*types.Chunk)

	// ==================== 第一次迭代：基本格式验证 + 文件内标准问去重 + 合并候选识别 ====================
	batchStandardQuestions := make(map[string]int) // value 为首次出现的索引
	validIndicesAfterStdQ := make([]int, 0, totalEntries)

	for i, entry := range entries {
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			fe := buildFAQFailedEntry(i, err.Error(), &entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 文件内标准问去重
		if firstIdx, exists := batchStandardQuestions[standardQ]; exists {
			progress.FailedCount++
			fe := buildFAQFailedEntry(i, fmt.Sprintf("标准问冲突：与批次内第 %d 条标准问重复", firstIdx+1), &entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			continue
		}

		// 判断是否为合并候选
		if chunk, exists := existingStdQToChunk[standardQ]; exists {
			// 标准问在 KB 中已存在 → 标记为合并候选
			mergeChunkMap[i] = chunk
			logger.Infof(ctx, "FAQ entry %d: standard question '%s' exists in KB, marking as merge candidate (chunk_id=%s)", i, standardQ, chunk.ID)
		} else if conflictChunkID, hit := existingQuestionToChunkID[standardQ]; hit {
			// 标准问未与任何已有标准问重复，但撞上了 KB 中其他条目的相似问
			// → 前置校验失败，避免下游 calculateAppendOperations 静默丢弃导致统计失真
			conflictStdQ := existingChunkIDToStdQ[conflictChunkID]
			progress.FailedCount++
			fe := buildFAQFailedEntry(i,
				fmt.Sprintf(`标准问冲突：与知识库中标准问"%s"的相似问"%s"重复`, conflictStdQ, standardQ),
				&entry)
			fe.FailureType = "pre_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			logger.Infof(ctx,
				"FAQ entry %d: standard question '%s' conflicts with existing similar question of chunk_id=%s (std='%s')",
				i, standardQ, conflictChunkID, conflictStdQ)
			continue
		}

		batchStandardQuestions[standardQ] = i
		validIndicesAfterStdQ = append(validIndicesAfterStdQ, i)

		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证标准问 %d/%d...", i+1, totalEntries)
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第二次迭代：相似问冲突检测 ====================
	// 构建批次内所有标准问 + 相似问的集合
	batchAllQuestions := make(map[string]int)
	for _, i := range validIndicesAfterStdQ {
		standardQ := strings.TrimSpace(entries[i].StandardQuestion)
		batchAllQuestions[standardQ] = i
		for _, q := range entries[i].SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				if _, exists := batchAllQuestions[q]; !exists {
					batchAllQuestions[q] = i
				}
			}
		}
	}

	removedSimilarQuestionsMap := make(map[int][]string)
	removedNegativeQuestionsMap := make(map[int][]string)

	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 合并候选：获取目标 chunk 自身的问题集合，用于排除
		var ownChunkQuestions map[string]bool
		if mergeChunk, isMerge := mergeChunkMap[i]; isMerge {
			ownChunkQuestions = existingChunkQuestions[mergeChunk.ID]
		}

		validSimilarQuestions := make([]string, 0, len(entry.SimilarQuestions))
		var removedSimilarQuestions []string
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 相似问不能与自己的标准问相同
			if q == standardQ {
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与本条"标准问"冲突`, q))
				continue
			}
			// 相似问校验：对比知识库已有标准问 + 相似问
			if _, exists := existingQuestionToChunkID[q]; exists {
				// 合并候选：如果问题属于合并目标自身的 chunk，允许（去重合并时会处理）
				if ownChunkQuestions != nil && ownChunkQuestions[q] {
					validSimilarQuestions = append(validSimilarQuestions, q)
					continue
				}
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与知识库已有"标准问/相似问"冲突`, q))
				continue
			}
			// 相似问校验：对比批次内标准问 + 相似问
			if firstIdx, exists := batchAllQuestions[q]; exists && firstIdx != i {
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与第 %d 行"标准问/相似问"冲突`, q, firstIdx+1))
				continue
			}
			validSimilarQuestions = append(validSimilarQuestions, q)
		}
		entries[i].SimilarQuestions = validSimilarQuestions

		if len(removedSimilarQuestions) > 0 {
			removedSimilarQuestionsMap[i] = removedSimilarQuestions
		}

		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证相似问 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第三次迭代：反例冲突检测（预校验，仅检查新条目自身数据） ====================
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		currentQAQuestions := make(map[string]bool)
		currentQAQuestions[standardQ] = true
		for _, q := range entry.SimilarQuestions {
			currentQAQuestions[q] = true
		}

		validNegativeQuestions := make([]string, 0, len(entry.NegativeQuestions))
		var removedNegativeQuestions []string
		for _, q := range entry.NegativeQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if currentQAQuestions[q] {
				removedNegativeQuestions = append(removedNegativeQuestions, fmt.Sprintf(`「反例冲突」："%s"与本条"标准问/相似问"冲突`, q))
				continue
			}
			validNegativeQuestions = append(validNegativeQuestions, q)
		}
		entries[i].NegativeQuestions = validNegativeQuestions

		if len(removedNegativeQuestions) > 0 {
			removedNegativeQuestionsMap[i] = removedNegativeQuestions
		}

		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证反例 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第四次迭代：后校验（仅合并候选） ====================
	// 对合并后的完整数据重跑反例校验，冲突则整条回退到合并前状态
	postValidationFailed := make(map[int]bool)
	mergeCount := 0
	for _, i := range validIndicesAfterStdQ {
		mergeChunk, isMerge := mergeChunkMap[i]
		if !isMerge {
			continue
		}

		existingMeta, err := mergeChunk.FAQMetadata()
		if err != nil || existingMeta == nil {
			logger.Warnf(ctx, "FAQ entry %d: failed to get merge target metadata, skipping post-validation", i)
			continue
		}

		entry := &entries[i]

		// 计算合并后的完整数据
		mergedSimilar := unionStrings(existingMeta.SimilarQuestions, entry.SimilarQuestions)
		mergedNegative := unionStrings(existingMeta.NegativeQuestions, entry.NegativeQuestions)

		// 构建合并后的冲突集合（标准问 + 所有合并后的相似问）
		mergedPositiveSet := make(map[string]bool)
		mergedPositiveSet[existingMeta.StandardQuestion] = true
		for _, q := range mergedSimilar {
			mergedPositiveSet[q] = true
		}

		// 检查每个合并后的反例是否与合并后的标准问 / 相似问冲突
		var conflictingNegatives []string
		for _, q := range mergedNegative {
			if mergedPositiveSet[q] {
				conflictingNegatives = append(conflictingNegatives, q)
			}
		}

		if len(conflictingNegatives) > 0 {
			// 后校验失败 → 整条回退到合并前状态
			postValidationFailed[i] = true
			delete(mergeChunkMap, i)
			progress.FailedCount++
			reason := fmt.Sprintf("后校验失败：合并后反例「%s」与相似问冲突", strings.Join(conflictingNegatives, "、"))
			fe := buildFAQFailedEntry(i, reason, entry)
			fe.FailureType = "post_validation"
			progress.FailedEntries = append(progress.FailedEntries, fe)
			logger.Infof(ctx, "FAQ entry %d: post-validation failed, conflicting negatives after merge: %v", i, conflictingNegatives)
		} else {
			mergeCount++
		}
	}

	// 从有效索引中移除后校验失败的条目
	if len(postValidationFailed) > 0 {
		filtered := make([]int, 0, len(validIndicesAfterStdQ))
		for _, i := range validIndicesAfterStdQ {
			if !postValidationFailed[i] {
				filtered = append(filtered, i)
			}
		}
		validIndicesAfterStdQ = filtered
	}

	// 将部分失败信息添加到 FailedEntries
	for _, i := range validIndicesAfterStdQ {
		removedSimilar := removedSimilarQuestionsMap[i]
		removedNegative := removedNegativeQuestionsMap[i]
		if len(removedSimilar) > 0 || len(removedNegative) > 0 {
			pf := buildFAQPartialFailedEntry(i, &entries[i], removedSimilar, removedNegative)
			progress.FailedEntries = append(progress.FailedEntries, pf)
			progress.PartialFailedCount++
		}
	}

	// 记录合并条目索引到 progress（用于执行阶段和重试）
	mergeIndices := make([]int, 0, mergeCount)
	for _, i := range validIndicesAfterStdQ {
		if _, isMerge := mergeChunkMap[i]; isMerge {
			mergeIndices = append(mergeIndices, i)
		}
	}
	progress.MergeEntryIndices = mergeIndices

	logger.Infof(ctx, "Append mode validation completed: total=%d, valid=%d, merge_candidates=%d, failed=%d, partial_failed=%d",
		totalEntries, len(validIndicesAfterStdQ), mergeCount, progress.FailedCount, progress.PartialFailedCount)

	return validIndicesAfterStdQ
}

// validateEntriesForReplaceModeWithProgress 验证 Replace 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
// 分三次迭代校验，确保前面过滤掉的数据不会被后面考虑：
// 1. 标准问 - 对比所有标准问 → 整条QA失败「标准问冲突」
// 2. 相似问 - 对比所有标准问+相似问 → 单条问法失败「相似问冲突」（仅移除冲突的相似问）
// 3. 反例 - 对比当前QA下所有标准问+相似问 → 单条问法失败「反例冲突」（仅移除冲突的反例）
func (s *knowledgeService) validateEntriesForReplaceModeWithProgress(ctx context.Context,
	entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	totalEntries := len(entries)

	// ==================== 第一次迭代：基本格式验证 + 标准问冲突检测 ====================
	// 标准问冲突会导致整条QA失败
	batchStandardQuestions := make(map[string]int) // value 为首次出现的索引
	validIndicesAfterStdQ := make([]int, 0, totalEntries)

	for i, entry := range entries {
		// 验证条目基本格式
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, err.Error(), &entry))
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 标准问校验：对比所有标准问 → 整条QA失败「标准问冲突」
		if firstIdx, exists := batchStandardQuestions[standardQ]; exists {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("标准问冲突：与批次内第 %d 条标准问重复", firstIdx+1), &entry))
			continue
		}

		// 记录标准问
		batchStandardQuestions[standardQ] = i
		validIndicesAfterStdQ = append(validIndicesAfterStdQ, i)

		// 定期更新进度消息
		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证标准问 %d/%d...", i+1, totalEntries)
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第二次迭代：相似问冲突检测 ====================
	// 只处理通过第一次校验的条目，相似问冲突只移除冲突的相似问
	// 构建所有标准问+相似问的集合（只包含通过第一次校验的条目）
	batchAllQuestions := make(map[string]int) // value 为首次出现的索引
	for _, i := range validIndicesAfterStdQ {
		standardQ := strings.TrimSpace(entries[i].StandardQuestion)
		batchAllQuestions[standardQ] = i
		for _, q := range entries[i].SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				// 只记录第一次出现的位置
				if _, exists := batchAllQuestions[q]; !exists {
					batchAllQuestions[q] = i
				}
			}
		}
	}

	// 用于收集每个条目被移除的相似问和反例
	removedSimilarQuestionsMap := make(map[int][]string)  // key 为条目索引
	removedNegativeQuestionsMap := make(map[int][]string) // key 为条目索引

	// 对每个通过第一次校验的条目，过滤冲突的相似问
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		validSimilarQuestions := make([]string, 0, len(entry.SimilarQuestions))
		var removedSimilarQuestions []string
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 相似问校验：对比所有标准问+相似问 → 单条问法失败「相似问冲突」
			// 如果该相似问与其他条目的标准问或相似问冲突（且不是自己的标准问），则移除
			if firstIdx, exists := batchAllQuestions[q]; exists && firstIdx != i {
				logger.Infof(ctx, "FAQ entry %d: similar question '%s' conflicts with entry %d, removing", i, q, firstIdx+1)
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与第 %d 行"标准问/相似问"冲突`, q, firstIdx+1))
				continue
			}
			// 相似问不能与自己的标准问相同
			if q == standardQ {
				logger.Infof(ctx, "FAQ entry %d: similar question '%s' same as standard question, removing", i, q)
				removedSimilarQuestions = append(removedSimilarQuestions, fmt.Sprintf(`「相似问冲突」："%s"与本条"标准问"冲突`, q))
				continue
			}
			validSimilarQuestions = append(validSimilarQuestions, q)
		}
		entries[i].SimilarQuestions = validSimilarQuestions

		// 记录被移除的相似问
		if len(removedSimilarQuestions) > 0 {
			removedSimilarQuestionsMap[i] = removedSimilarQuestions
		}

		// 定期更新进度消息
		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证相似问 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// ==================== 第三次迭代：反例冲突检测 ====================
	// 只处理通过前两次校验的条目，反例冲突只移除冲突的反例
	for idx, i := range validIndicesAfterStdQ {
		entry := &entries[i]
		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 构建当前QA的所有问题集合（标准问+通过校验的相似问）
		currentQAQuestions := make(map[string]bool)
		currentQAQuestions[standardQ] = true
		for _, q := range entry.SimilarQuestions {
			currentQAQuestions[q] = true
		}

		validNegativeQuestions := make([]string, 0, len(entry.NegativeQuestions))
		var removedNegativeQuestions []string
		for _, q := range entry.NegativeQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 反例校验：对比当前QA下所有标准问+相似问 → 单条问法失败「反例冲突」
			if currentQAQuestions[q] {
				logger.Infof(ctx, "FAQ entry %d: negative question '%s' conflicts with current QA's questions, removing", i, q)
				removedNegativeQuestions = append(removedNegativeQuestions, fmt.Sprintf(`「反例冲突」："%s"与本条"标准问/相似问"冲突`, q))
				continue
			}
			validNegativeQuestions = append(validNegativeQuestions, q)
		}
		entries[i].NegativeQuestions = validNegativeQuestions

		// 记录被移除的反例
		if len(removedNegativeQuestions) > 0 {
			removedNegativeQuestionsMap[i] = removedNegativeQuestions
		}

		// 定期更新进度消息
		if (idx+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证反例 %d/%d...", idx+1, len(validIndicesAfterStdQ))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	// 将部分失败信息添加到 FailedEntries
	for _, i := range validIndicesAfterStdQ {
		removedSimilar := removedSimilarQuestionsMap[i]
		removedNegative := removedNegativeQuestionsMap[i]
		if len(removedSimilar) > 0 || len(removedNegative) > 0 {
			pf := buildFAQPartialFailedEntry(i, &entries[i], removedSimilar, removedNegative)
			progress.FailedEntries = append(progress.FailedEntries, pf)
			progress.PartialFailedCount++
		}
	}

	return validIndicesAfterStdQ
}

// unionStrings 合并两个字符串切片并去重（完全匹配）
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// validateFAQEntryPayloadBasic 验证 FAQ 条目的基本格式
func validateFAQEntryPayloadBasic(entry *types.FAQEntryPayload) error {
	if entry == nil {
		return fmt.Errorf("条目不能为空")
	}
	standardQ := strings.TrimSpace(entry.StandardQuestion)
	if standardQ == "" {
		return fmt.Errorf("标准问不能为空")
	}
	if len(entry.Answers) == 0 {
		return fmt.Errorf("答案不能为空")
	}
	hasValidAnswer := false
	for _, a := range entry.Answers {
		if strings.TrimSpace(a) != "" {
			hasValidAnswer = true
			break
		}
	}
	if !hasValidAnswer {
		return fmt.Errorf("答案不能全为空")
	}
	return nil
}

type faqMergeOperation struct {
	Entry         types.FAQEntryPayload
	ExistingChunk *types.Chunk
	OldMeta       *types.FAQChunkMetadata
	MergedMeta    *types.FAQChunkMetadata
	Detail        types.FAQMergeDetail
}

// calculateAppendOperations 计算 Append 模式下的操作（支持智能合并）。
// 如果 entry 的标准问在 KB 中已存在，则视为合并操作（相似问 / 反例并集，
// 答案 / 策略以新条目为准）；否则视为新建。无变化（hash 一致且操作位
// 也未变）的合并目标会被 skip 掉，避免无效写库 / 重建索引。
//
// 内部 master 行为，对应开源版仅 "重复 = 失败" 的简化逻辑。FAQ 导入的
// 常见使用方式是"导出修改后重新 append"，需要这种合并语义才能正确叠加
// 新的相似问而不丢历史数据。
func (s *knowledgeService) calculateAppendOperations(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload,
) (newEntries []types.FAQEntryPayload, mergeOps []faqMergeOperation, skippedCount int, err error) {
	if len(entries) == 0 {
		return nil, nil, 0, nil
	}

	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to list existing FAQ chunks: %w", err)
	}

	existingStdQToChunk := make(map[string]*types.Chunk)
	existingQuestions := make(map[string]bool)
	for _, chunk := range existingChunks {
		meta, cErr := chunk.FAQMetadata()
		if cErr != nil || meta == nil {
			continue
		}
		if meta.StandardQuestion != "" {
			existingStdQToChunk[meta.StandardQuestion] = chunk
			existingQuestions[meta.StandardQuestion] = true
		}
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestions[q] = true
			}
		}
	}

	batchQuestions := make(map[string]bool)
	newEntries = make([]types.FAQEntryPayload, 0, len(entries))
	mergeOps = make([]faqMergeOperation, 0)

	for entryIdx, entry := range entries {
		meta, sErr := sanitizeFAQEntryPayload(&entry)
		if sErr != nil {
			skippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry: %v", sErr)
			continue
		}

		// 检查标准问是否在 KB 中作为标准问存在 → 合并候选
		existingChunk, isMergeCandidate := existingStdQToChunk[meta.StandardQuestion]
		if isMergeCandidate {
			existingMeta, mErr := existingChunk.FAQMetadata()
			if mErr != nil || existingMeta == nil {
				isMergeCandidate = false
			} else {
				mergedSimilar := unionStrings(existingMeta.SimilarQuestions, meta.SimilarQuestions)
				mergedNegative := unionStrings(existingMeta.NegativeQuestions, meta.NegativeQuestions)

				mergedMeta := &types.FAQChunkMetadata{
					StandardQuestion:  existingMeta.StandardQuestion,
					SimilarQuestions:  mergedSimilar,
					NegativeQuestions: mergedNegative,
					Answers:           meta.Answers,
					AnswerStrategy:    meta.AnswerStrategy,
					Version:           existingMeta.Version + 1,
					Source:            existingMeta.Source,
				}

				newHash := types.CalculateFAQContentHash(mergedMeta)
				enabledChanged := entry.IsEnabled != nil && *entry.IsEnabled != existingChunk.IsEnabled
				recommendedChanged := entry.IsRecommended != nil &&
					*entry.IsRecommended != existingChunk.Flags.HasFlag(types.ChunkFlagRecommended)
				answerStrategyChanged := meta.AnswerStrategy != existingMeta.AnswerStrategy
				if existingChunk.ContentHash == newHash && !enabledChanged && !recommendedChanged && !answerStrategyChanged {
					skippedCount++
					logger.Infof(ctx, "Skipping merge for unchanged FAQ entry: %s", meta.StandardQuestion)
					continue
				}

				oldAnswerStr := strings.Join(existingMeta.Answers, "##")
				newAnswerStr := strings.Join(meta.Answers, "##")
				oldSimilarSet := make(map[string]bool, len(existingMeta.SimilarQuestions))
				for _, q := range existingMeta.SimilarQuestions {
					oldSimilarSet[q] = true
				}
				oldNegativeSet := make(map[string]bool, len(existingMeta.NegativeQuestions))
				for _, q := range existingMeta.NegativeQuestions {
					oldNegativeSet[q] = true
				}
				newSimilarCount := 0
				for _, q := range mergedSimilar {
					if !oldSimilarSet[q] {
						newSimilarCount++
					}
				}
				newNegativeCount := 0
				for _, q := range mergedNegative {
					if !oldNegativeSet[q] {
						newNegativeCount++
					}
				}

				mergeOps = append(mergeOps, faqMergeOperation{
					Entry:         entry,
					ExistingChunk: existingChunk,
					OldMeta:       existingMeta,
					MergedMeta:    mergedMeta,
					Detail: types.FAQMergeDetail{
						Index:            entryIdx,
						StandardQuestion: meta.StandardQuestion,
						AnswerChanged:    oldAnswerStr != newAnswerStr,
						NewSimilarCount:  newSimilarCount,
						NewNegativeCount: newNegativeCount,
					},
				})
				continue
			}
		}

		// 走到这里说明既不是合并候选，又跟现有 KB / 当前批次的标准问 / 相似问冲突。
		// 正常情况下这些冲突应在 executeFAQDryRunValidation 阶段就被拦截，这里
		// 仅作为防御性兜底；用 Warn 级别打点便于排查验证层漏检。
		if existingQuestions[meta.StandardQuestion] || batchQuestions[meta.StandardQuestion] {
			skippedCount++
			logger.Warnf(ctx,
				"calculateAppendOperations: dropping FAQ entry with duplicate standard question %q "+
					"(should have been filtered at validation); entry_idx=%d",
				meta.StandardQuestion, entryIdx)
			continue
		}

		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}
		newEntries = append(newEntries, entry)
	}

	return newEntries, mergeOps, skippedCount, nil
}

// calculateReplaceOperations 计算Replace模式下需要删除、创建、更新的条目
// 同时过滤掉同批次内标准问或相似问重复的条目
func (s *knowledgeService) calculateReplaceOperations(ctx context.Context,
	tenantID uint64, knowledgeID string, newEntries []types.FAQEntryPayload,
) ([]types.FAQEntryPayload, []*types.Chunk, int, error) {
	// 获取 kbID 用于解析 tag
	var kbID string
	if len(newEntries) > 0 {
		// 从 knowledgeID 获取 kbID
		knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to get knowledge: %w", err)
		}
		if knowledge != nil {
			kbID = knowledge.KnowledgeBaseID
		}
	}

	// 计算所有新条目的 content hash，并同时构建 hash 到 entry 的映射
	type entryWithHash struct {
		entry types.FAQEntryPayload
		hash  string
		meta  *types.FAQChunkMetadata
	}
	entriesWithHash := make([]entryWithHash, 0, len(newEntries))
	newHashSet := make(map[string]bool)
	// 用于批次内标准问和相似问去重
	batchQuestions := make(map[string]bool)
	batchSkippedCount := 0

	for _, entry := range newEntries {
		meta, err := sanitizeFAQEntryPayload(&entry)
		if err != nil {
			batchSkippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry in replace mode: %v", err)
			continue
		}

		// 检查标准问是否在同批次中重复
		if batchQuestions[meta.StandardQuestion] {
			batchSkippedCount++
			logger.Infof(ctx, "Skipping FAQ entry with duplicate standard question in batch: %s", meta.StandardQuestion)
			continue
		}

		// 检查相似问是否在同批次中重复
		hasDuplicateSimilar := false
		for _, q := range meta.SimilarQuestions {
			if batchQuestions[q] {
				hasDuplicateSimilar = true
				logger.Infof(ctx, "Skipping FAQ entry with duplicate similar question in batch: %s (standard: %s)", q, meta.StandardQuestion)
				break
			}
		}
		if hasDuplicateSimilar {
			batchSkippedCount++
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}

		hash := types.CalculateFAQContentHash(meta)
		if hash != "" {
			entriesWithHash = append(entriesWithHash, entryWithHash{entry: entry, hash: hash, meta: meta})
			newHashSet[hash] = true
		}
	}

	// 查询所有已存在的chunks
	allExistingChunks, err := s.chunkRepo.ListAllFAQChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to list existing chunks: %w", err)
	}

	// 在内存中过滤出匹配新条目hash的chunks，并构建map
	existingHashMap := make(map[string]*types.Chunk)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash != "" && newHashSet[chunk.ContentHash] {
			existingHashMap[chunk.ContentHash] = chunk
		}
	}

	// 计算需要删除的chunks（数据库中有但新批次中没有的，或hash不匹配的）
	chunksToDelete := make([]*types.Chunk, 0)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash == "" {
			// 如果没有hash，需要删除（可能是旧数据）
			chunksToDelete = append(chunksToDelete, chunk)
		} else if !newHashSet[chunk.ContentHash] {
			// hash不在新条目中，需要删除
			chunksToDelete = append(chunksToDelete, chunk)
		}
	}

	// 批量预加载 tag 信息，避免循环内逐条查询数据库
	// 收集所有需要查询的 tag_id 和 tag_name
	tagSeqIDSet := make(map[int64]bool)
	tagNameSet := make(map[string]bool)
	for _, ewh := range entriesWithHash {
		if ewh.entry.TagID != 0 {
			tagSeqIDSet[ewh.entry.TagID] = true
		} else if ewh.entry.TagName != "" {
			tagNameSet[ewh.entry.TagName] = true
		} else {
			tagNameSet[types.UntaggedTagName] = true
		}
	}

	// 批量查询 tag by seq_id
	tagSeqIDToUUID := make(map[int64]string)
	if len(tagSeqIDSet) > 0 {
		seqIDs := make([]int64, 0, len(tagSeqIDSet))
		for seqID := range tagSeqIDSet {
			seqIDs = append(seqIDs, seqID)
		}
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, seqIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to batch load tags by seq_ids: %v", err)
		} else {
			for _, tag := range tags {
				tagSeqIDToUUID[tag.SeqID] = tag.ID
			}
		}
	}

	// 批量查询 tag by name
	tagNameToUUID := make(map[string]string)
	if len(tagNameSet) > 0 && kbID != "" {
		for name := range tagNameSet {
			if tag, err := s.tagRepo.GetByName(ctx, tenantID, kbID, name); err == nil && tag != nil {
				tagNameToUUID[name] = tag.ID
			}
		}
	}

	logger.Infof(ctx, "Preloaded %d tags by seq_id, %d tags by name for %d entries",
		len(tagSeqIDToUUID), len(tagNameToUUID), len(entriesWithHash))

	// resolveTagIDFromCache 从缓存中解析 tag ID，缓存未命中时回退到数据库查询
	resolveTagIDFromCache := func(entry *types.FAQEntryPayload) (string, error) {
		if entry.TagID != 0 {
			if uuid, ok := tagSeqIDToUUID[entry.TagID]; ok {
				return uuid, nil
			}
			// 缓存未命中，回退到数据库查询（可能需要创建）
			return s.resolveTagID(ctx, kbID, entry)
		}
		tagName := entry.TagName
		if tagName == "" {
			tagName = types.UntaggedTagName
		}
		if uuid, ok := tagNameToUUID[tagName]; ok {
			return uuid, nil
		}
		// 缓存未命中，回退到数据库查询（可能需要创建）
		return s.resolveTagID(ctx, kbID, entry)
	}

	// 计算需要创建的条目（利用已经计算好的hash，避免重复计算）
	entriesToProcess := make([]types.FAQEntryPayload, 0, len(entriesWithHash))
	skippedCount := batchSkippedCount

	for idx, ewh := range entriesWithHash {
		// 每处理 1000 条打印一次进度日志
		if idx > 0 && idx%1000 == 0 {
			logger.Infof(ctx, "calculateReplaceOperations progress: %d/%d entries processed", idx, len(entriesWithHash))
		}

		existingChunk := existingHashMap[ewh.hash]
		if existingChunk != nil {
			// hash 匹配，检查 tag 是否变化
			newTagID, err := resolveTagIDFromCache(&ewh.entry)
			if err != nil {
				logger.Warnf(ctx, "Failed to resolve tag for entry, treating as new: %v", err)
				entriesToProcess = append(entriesToProcess, ewh.entry)
				continue
			}

			enabledChanged := ewh.entry.IsEnabled != nil && *ewh.entry.IsEnabled != existingChunk.IsEnabled
			recommendedChanged := ewh.entry.IsRecommended != nil &&
				*ewh.entry.IsRecommended != existingChunk.Flags.HasFlag(types.ChunkFlagRecommended)
			answerStrategyChanged := false
			if existingMeta, metaErr := existingChunk.FAQMetadata(); metaErr == nil && existingMeta != nil {
				answerStrategyChanged = ewh.meta.AnswerStrategy != existingMeta.AnswerStrategy
			}

			if existingChunk.TagID != newTagID || enabledChanged || recommendedChanged || answerStrategyChanged {
				if existingChunk.TagID != newTagID {
					logger.Infof(ctx, "FAQ entry tag changed from %s to %s, will update", existingChunk.TagID, newTagID)
				}
				if enabledChanged || recommendedChanged || answerStrategyChanged {
					logger.Infof(ctx, "FAQ entry operational fields changed (enabled: %v->%v, recommended: %v->%v, answerStrategy changed: %v), will update",
						existingChunk.IsEnabled, ewh.entry.IsEnabled,
						existingChunk.Flags.HasFlag(types.ChunkFlagRecommended), ewh.entry.IsRecommended,
						answerStrategyChanged)
				}
				chunksToDelete = append(chunksToDelete, existingChunk)
				entriesToProcess = append(entriesToProcess, ewh.entry)
			} else {
				// hash、tag、运营状态都相同，跳过
				skippedCount++
			}
			continue
		}

		// hash 不匹配或不存在，需要创建
		entriesToProcess = append(entriesToProcess, ewh.entry)
	}

	return entriesToProcess, chunksToDelete, skippedCount, nil
}

// executeFAQImport 执行实际的FAQ导入逻辑
func (s *knowledgeService) executeFAQImport(ctx context.Context, taskID string, kbID string,
	payload *types.FAQBatchUpsertPayload, tenantID uint64, processedCount int,
	progress *types.FAQImportProgress,
) (err error) {
	// 保存知识库和embedding模型信息，用于清理索引
	var kb *types.KnowledgeBase
	var embeddingModel embedding.Embedder
	totalEntries := len(payload.Entries) + processedCount

	// Recovery机制：如果发生任何错误或panic，回滚所有已创建的chunks和索引数据
	defer func() {
		// 捕获panic
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			stack := string(buf[:n])
			logger.Errorf(ctx, "FAQ import task %s panicked: %v\n%s", taskID, r, stack)
			err = fmt.Errorf("panic during FAQ import: %v", r)
		}
	}()

	kb, err = s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}

	kb.EnsureDefaults()

	// 获取embedding模型，用于后续清理索引
	embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return fmt.Errorf("failed to get embedding model: %w", err)
	}
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return err
	}

	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 增量更新逻辑：计算需要处理的条目
	var entriesToProcess []types.FAQEntryPayload
	var chunksToDelete []*types.Chunk
	var skippedCount int

	if payload.Mode == types.FAQBatchModeReplace {
		// Replace模式：计算需要删除、创建、更新的条目
		entriesToProcess, chunksToDelete, skippedCount, err = s.calculateReplaceOperations(
			ctx,
			tenantID,
			faqKnowledge.ID,
			payload.Entries,
		)
		if err != nil {
			return fmt.Errorf("failed to calculate replace operations: %w", err)
		}

		// 删除需要删除的chunks（包括需要更新的旧chunks）
		if len(chunksToDelete) > 0 {
			chunkIDsToDelete := make([]string, 0, len(chunksToDelete))
			for _, chunk := range chunksToDelete {
				chunkIDsToDelete = append(chunkIDsToDelete, chunk.ID)
			}
			if err := s.chunkRepo.DeleteChunks(ctx, tenantID, chunkIDsToDelete); err != nil {
				return fmt.Errorf("failed to delete chunks: %w", err)
			}
			// 删除索引
			if err := s.deleteFAQChunkVectors(ctx, kb, faqKnowledge, chunksToDelete); err != nil {
				return fmt.Errorf("failed to delete chunk vectors: %w", err)
			}
			logger.Infof(ctx, "FAQ import task %s: deleted %d chunks (including updates)", taskID, len(chunksToDelete))
		}
	} else {
		// Append 模式（智能合并）：标准问已存在的条目走 merge ops，其余作为新建。
		var mergeOps []faqMergeOperation
		entriesToProcess, mergeOps, skippedCount, err = s.calculateAppendOperations(ctx, tenantID, kb.ID, payload.Entries)
		if err != nil {
			return fmt.Errorf("failed to calculate append operations: %w", err)
		}

		if len(mergeOps) > 0 {
			mergedCount, mergeErr := s.executeFAQMergeOperations(ctx, taskID, kb, faqKnowledge, embeddingModel, indexMode, mergeOps, progress)
			if mergeErr != nil {
				return fmt.Errorf("failed to execute merge operations: %w", mergeErr)
			}
			logger.Infof(ctx, "FAQ import task %s: merged %d entries", taskID, mergedCount)
			progress.MergedCount = mergedCount
			for _, op := range mergeOps {
				progress.MergeDetails = append(progress.MergeDetails, op.Detail)
			}
		}
	}
	logger.Infof(
		ctx,
		"FAQ import task %s: total entries: %d, new to create: %d, skipped: %d, merged: %d",
		taskID,
		len(payload.Entries),
		len(entriesToProcess),
		skippedCount,
		progress.MergedCount,
	)

	// 如果没有需要处理的条目，直接返回
	if len(entriesToProcess) == 0 {
		logger.Infof(ctx, "FAQ import task %s: no new entries to create", taskID)
		return nil
	}

	// 分批处理需要创建的条目
	remainingEntries := len(entriesToProcess)
	totalStartTime := time.Now()
	actualProcessed := skippedCount + processedCount + progress.MergedCount

	logger.Infof(
		ctx,
		"FAQ import task %s: starting batch processing, remaining entries: %d, total entries: %d, batch size: %d",
		taskID,
		remainingEntries,
		totalEntries,
		faqImportBatchSize,
	)

	for i := 0; i < remainingEntries; i += faqImportBatchSize {
		batchStartTime := time.Now()
		end := i + faqImportBatchSize
		if end > remainingEntries {
			end = remainingEntries
		}

		batch := entriesToProcess[i:end]
		logger.Infof(ctx, "FAQ import task %s: processing batch %d-%d (%d entries)", taskID, i+1, end, len(batch))

		// 构建chunks
		buildStartTime := time.Now()
		chunks := make([]*types.Chunk, 0, len(batch))
		chunkIds := make([]string, 0, len(batch))
		for idx, entry := range batch {
			meta, err := sanitizeFAQEntryPayload(&entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to sanitize entry at index %d: %w", i+idx, err)
			}

			// 解析 TagID
			tagID, err := s.resolveTagID(ctx, kbID, &entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to resolve tag for entry at index %d: %w", i+idx, err)
			}

			isEnabled := true
			if entry.IsEnabled != nil {
				isEnabled = *entry.IsEnabled
			}
			// ChunkIndex计算：(i+idx) — FAQ entries are new, start from 0
			chunk := &types.Chunk{
				ID:              ComputeChunkStableID(faqKnowledge.ID, "text", i+idx, buildFAQChunkContent(meta, indexMode)),
				TenantID:        tenantID,
				KnowledgeID:     faqKnowledge.ID,
				KnowledgeBaseID: kb.ID,
				Content:         buildFAQChunkContent(meta, indexMode),
				// ChunkIndex:      0,
				IsEnabled: isEnabled,
				ChunkType: types.ChunkTypeFAQ,
				TagID:     tagID,                        // 使用解析后的 TagID
				Status:    int(types.ChunkStatusStored), // store but not indexed
			}
			// 如果指定了 ID（用于数据迁移），设置 SeqID
			if entry.ID != nil && *entry.ID > 0 {
				chunk.SeqID = *entry.ID
			}
			if err := chunk.SetFAQMetadata(meta); err != nil {
				return fmt.Errorf("failed to set FAQ metadata: %w", err)
			}
			chunks = append(chunks, chunk)
			chunkIds = append(chunkIds, chunk.ID)
		}
		buildDuration := time.Since(buildStartTime)
		logger.Debugf(ctx, "FAQ import task %s: batch %d-%d built %d chunks in %v, chunk IDs: %v",
			taskID, i+1, end, len(chunks), buildDuration, chunkIds)
		// 创建chunks
		createStartTime := time.Now()
		if err := s.chunkService.CreateChunks(ctx, chunks); err != nil {
			return fmt.Errorf("failed to create chunks: %w", err)
		}
		createDuration := time.Since(createStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d created %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			createDuration,
		)

		// 索引chunks
		indexStartTime := time.Now()
		// 注意：如果索引失败，defer中的recovery机制会自动回滚已创建的chunks和索引数据
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, chunks, embeddingModel, true, false); err != nil {
			return fmt.Errorf("failed to index chunks: %w", err)
		}
		indexDuration := time.Since(indexStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d indexed %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			indexDuration,
		)

		// 更新chunks的Status为已索引
		chunksToUpdate := make([]*types.Chunk, 0, len(chunks))
		for _, chunk := range chunks {
			chunk.Status = int(types.ChunkStatusIndexed) // indexed
			chunksToUpdate = append(chunksToUpdate, chunk)
		}
		if err := s.chunkService.UpdateChunks(ctx, chunksToUpdate); err != nil {
			return fmt.Errorf("failed to update chunks status: %w", err)
		}

		// 收集成功条目信息
		for idx, chunk := range chunks {
			entryIdx := i + idx + processedCount // 原始条目索引
			meta, _ := chunk.FAQMetadata()
			standardQ := ""
			if meta != nil {
				standardQ = meta.StandardQuestion
			}
			// 获取 tag info
			var tagID int64
			tagName := ""
			if chunk.TagID != "" {
				if tag, err := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID); err == nil && tag != nil {
					tagID = tag.SeqID
					tagName = tag.Name
				}
			}
			progress.SuccessEntries = append(progress.SuccessEntries, types.FAQSuccessEntry{
				Index:            entryIdx,
				SeqID:            chunk.SeqID,
				TagID:            tagID,
				TagName:          tagName,
				StandardQuestion: standardQ,
			})
		}

		actualProcessed += len(batch)
		// 更新任务进度
		progress := int(float64(actualProcessed) / float64(totalEntries) * 100)
		if err := s.updateFAQImportProgressStatus(ctx, taskID, "", 0, types.FAQImportStatusProcessing, progress, totalEntries, actualProcessed, fmt.Sprintf("正在处理第 %d/%d 条", actualProcessed, totalEntries), ""); err != nil {
			logger.Errorf(ctx, "Failed to update task progress: %v", err)
		}

		batchDuration := time.Since(batchStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d completed in %v (build: %v, create: %v, index: %v), total progress: %d/%d (%d%%)",
			taskID,
			i+1,
			end,
			batchDuration,
			buildDuration,
			createDuration,
			indexDuration,
			actualProcessed,
			totalEntries,
			progress,
		)
	}

	totalDuration := time.Since(totalStartTime)
	var avgPerEntry time.Duration
	if actualProcessed > 0 {
		avgPerEntry = totalDuration / time.Duration(actualProcessed)
	}
	logger.Infof(
		ctx,
		"FAQ import task %s: all batches completed, processed: %d entries (skipped: %d) in %v, avg: %v per entry",
		taskID,
		actualProcessed,
		skippedCount,
		totalDuration,
		avgPerEntry,
	)

	return nil
}

// updateFAQImportProgressStatus updates the FAQ import progress in Redis
func (s *knowledgeService) updateFAQImportProgressStatus(
	ctx context.Context,
	taskID string,
	instanceID string,
	enqueuedAt int64,
	status types.FAQImportTaskStatus,
	progress, total, processed int,
	message, errorMsg string,
) error {
	// Get existing progress from Redis
	existingProgress, err := s.GetFAQImportProgress(ctx, taskID)
	if err != nil {
		// If not found, create a new progress entry
		existingProgress = &types.FAQImportProgress{
			TaskID:    taskID,
			CreatedAt: time.Now().Unix(),
		}
	}

	// Update progress fields
	existingProgress.Status = status
	existingProgress.Progress = progress
	existingProgress.Total = total
	existingProgress.Processed = processed
	if message != "" {
		existingProgress.Message = message
	}
	existingProgress.Error = errorMsg
	if status == types.FAQImportStatusCompleted {
		existingProgress.Error = ""
	}

	// 任务完成或失败时，清除 running key
	if status == types.FAQImportStatusCompleted || status == types.FAQImportStatusFailed {
		if existingProgress.KBID != "" {
			if clearErr := s.clearRunningFAQImportInfoIfMatches(ctx, existingProgress.KBID, taskID, instanceID, enqueuedAt); clearErr != nil {
				logger.Errorf(ctx, "Failed to clear running FAQ import task ID: %v", clearErr)
			}
		}
	}

	return s.saveFAQImportProgress(ctx, existingProgress)
}

// cleanupFAQEntriesFileOnFinalFailure 在任务最终失败时清理对象存储中的 entries 文件
// 只有当 retryCount >= maxRetry 时才执行清理，否则重试时还需要使用这个文件
func (s *knowledgeService) cleanupFAQEntriesFileOnFinalFailure(ctx context.Context, entriesURL string, retryCount, maxRetry int) {
	if entriesURL == "" || retryCount < maxRetry {
		return
	}
	if err := s.fileSvc.DeleteFile(ctx, entriesURL); err != nil {
		logger.Warnf(ctx, "Failed to delete FAQ entries file from object storage on final failure: %v", err)
	} else {
		logger.Infof(ctx, "Deleted FAQ entries file from object storage on final failure: %s", entriesURL)
	}
}

// runningFAQImportInfo stores the task ID and enqueued timestamp for uniquely identifying a task instance
type runningFAQImportInfo struct {
	TaskID     string `json:"task_id"`
	EnqueuedAt int64  `json:"enqueued_at"`
	InstanceID string `json:"instance_id,omitempty"`
}

// getRunningFAQImportInfo checks if there's a running FAQ import task for the given KB
// Returns the task info if found, nil otherwise
func (s *knowledgeService) getRunningFAQImportInfo(ctx context.Context, kbID string) (*runningFAQImportInfo, error) {
	if s.redisClient == nil {
		if v, ok := s.memFAQRunningImport.Load(kbID); ok {
			return v.(*runningFAQImportInfo), nil
		}
		return nil, nil
	}
	key := getFAQImportRunningKey(kbID)
	data, err := s.redisClient.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get running FAQ import task: %w", err)
	}

	// Try to parse as JSON first (new format)
	var info runningFAQImportInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		// Fallback: old format was just taskID string
		return &runningFAQImportInfo{TaskID: data, EnqueuedAt: 0}, nil
	}
	return &info, nil
}

// getRunningFAQImportTaskID checks if there's a running FAQ import task for the given KB
// Returns the task ID if found, empty string otherwise (for backward compatibility)
func (s *knowledgeService) getRunningFAQImportTaskID(ctx context.Context, kbID string) (string, error) {
	info, err := s.getRunningFAQImportInfo(ctx, kbID)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}
	return info.TaskID, nil
}

// setRunningFAQImportInfo sets the running task info for a KB
func (s *knowledgeService) setRunningFAQImportInfo(ctx context.Context, kbID string, info *runningFAQImportInfo) error {
	if s.redisClient == nil {
		s.memFAQRunningImport.Store(kbID, info)
		return nil
	}
	key := getFAQImportRunningKey(kbID)
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal running info: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, faqImportProgressTTL).Err()
}

// clearRunningFAQImportTaskID clears the running task ID for a KB
func (s *knowledgeService) clearRunningFAQImportTaskID(ctx context.Context, kbID string) error {
	if s.redisClient == nil {
		s.memFAQRunningImport.Delete(kbID)
		return nil
	}
	key := getFAQImportRunningKey(kbID)
	return s.redisClient.Del(ctx, key).Err()
}

func (s *knowledgeService) clearRunningFAQImportInfoIfMatches(ctx context.Context, kbID, taskID, instanceID string, enqueuedAt int64) error {
	if s.redisClient == nil {
		if v, ok := s.memFAQRunningImport.Load(kbID); ok {
			info, _ := v.(*runningFAQImportInfo)
			if runningFAQImportInfoMatches(info, taskID, instanceID, enqueuedAt) {
				s.memFAQRunningImport.Delete(kbID)
			}
		}
		return nil
	}

	info, err := s.getRunningFAQImportInfo(ctx, kbID)
	if err != nil {
		return err
	}
	if !runningFAQImportInfoMatches(info, taskID, instanceID, enqueuedAt) {
		return nil
	}

	key := getFAQImportRunningKey(kbID)
	return s.redisClient.Del(ctx, key).Err()
}

func runningFAQImportInfoMatches(info *runningFAQImportInfo, taskID, instanceID string, enqueuedAt int64) bool {
	if info == nil || info.TaskID != taskID {
		return false
	}
	if info.InstanceID != "" && instanceID != "" {
		return info.InstanceID == instanceID
	}
	return enqueuedAt == 0 || info.EnqueuedAt == 0 || info.EnqueuedAt == enqueuedAt
}

// incrementalIndexFAQEntry 增量更新FAQ条目的索引
// 只对内容变化的部分进行embedding计算和索引更新，跳过未变化的部分
func (s *knowledgeService) incrementalIndexFAQEntry(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	chunk *types.Chunk,
	embeddingModel embedding.Embedder,
	oldStandardQuestion string,
	oldSimilarQuestions []string,
	oldAnswers []string,
	newMeta *types.FAQChunkMetadata,
) error {
	indexStartTime := time.Now()
	logger.Debugf(ctx, "incrementalIndexFAQEntry: starting for chunk=%s, oldSimilarQuestions=%d, newSimilarQuestions=%d",
		chunk.ID, len(oldSimilarQuestions), len(newMeta.SimilarQuestions))

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, types.MustTenantIDFromContext(ctx), kb.VectorStoreID)
	if err != nil {
		return err
	}

	indexMode := types.FAQIndexModeQuestionAnswer
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 对新旧数据进行归一化处理，确保与 buildFAQIndexInfoList 的行为一致
	// 旧数据归一化
	oldStandardQuestion = types.NormalizeQuestion(oldStandardQuestion)
	normalizedOldSimilarQuestions := make([]string, 0, len(oldSimilarQuestions))
	for _, q := range oldSimilarQuestions {
		if nq := types.NormalizeQuestion(q); nq != "" {
			normalizedOldSimilarQuestions = append(normalizedOldSimilarQuestions, nq)
		}
	}
	oldSimilarQuestions = normalizedOldSimilarQuestions
	oldAnswers = types.SanitizeStrings(oldAnswers)
	// 新数据归一化
	normalizedNewMeta := newMeta.Normalize()

	// 构建索引内容
	buildContent := func(question string, answers []string) string {
		if indexMode == types.FAQIndexModeQuestionAnswer && len(answers) > 0 {
			var builder strings.Builder
			builder.WriteString(question)
			for _, ans := range answers {
				builder.WriteString("\n")
				builder.WriteString(ans)
			}
			return builder.String()
		}
		return question
	}

	// 检查答案是否变化（仅在 QuestionAnswer 模式下才影响索引）
	answersChanged := indexMode == types.FAQIndexModeQuestionAnswer && !slices.Equal(oldAnswers, normalizedNewMeta.Answers)
	logger.Debugf(ctx, "incrementalIndexFAQEntry: answersChanged=%v (indexMode=%s), oldAnswers=%d, newAnswers=%d",
		answersChanged, indexMode, len(oldAnswers), len(normalizedNewMeta.Answers))

	// 收集需要更新的索引项
	var indexInfoToUpdate []*types.IndexInfo

	// 1. 检查标准问是否需要更新
	oldStdContent := buildContent(oldStandardQuestion, oldAnswers)
	newStdContent := buildContent(normalizedNewMeta.StandardQuestion, normalizedNewMeta.Answers)
	stdQuestionChanged := oldStdContent != newStdContent
	if stdQuestionChanged {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: standard question changed, sourceID=%s", chunk.ID)
		indexInfoToUpdate = append(indexInfoToUpdate, &types.IndexInfo{
			Content:         newStdContent,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   types.KnowledgeTypeFAQ,
			TagID:           chunk.TagID,
			IsEnabled:       chunk.IsEnabled,
			IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
		})
	}

	// 2. 基于内容哈希处理相似问的增删改
	// 构建旧问题集合 (问题 -> 是否存在)
	oldQuestionsSet := make(map[string]struct{}, len(oldSimilarQuestions))
	for _, q := range oldSimilarQuestions {
		oldQuestionsSet[q] = struct{}{}
	}

	// 构建新问题集合
	newQuestionsSet := make(map[string]struct{}, len(normalizedNewMeta.SimilarQuestions))
	for _, q := range normalizedNewMeta.SimilarQuestions {
		newQuestionsSet[q] = struct{}{}
	}

	// 找出需要删除的问题（在旧集合中但不在新集合中）
	var sourceIDsToDelete []string
	var deletedQuestions []string
	for oldQ := range oldQuestionsSet {
		if _, exists := newQuestionsSet[oldQ]; !exists {
			sourceID := fmt.Sprintf("%s-%s", chunk.ID, hashQuestion(oldQ))
			sourceIDsToDelete = append(sourceIDsToDelete, sourceID)
			deletedQuestions = append(deletedQuestions, oldQ)
		}
	}

	// 找出需要新增或更新的问题
	var addedQuestions, updatedQuestions []string
	for newQ := range newQuestionsSet {
		_, existedBefore := oldQuestionsSet[newQ]
		// 需要更新的条件：
		// 1. 新问题（之前不存在）
		// 2. 答案变化（需要重新embedding）
		if !existedBefore || answersChanged {
			sourceID := fmt.Sprintf("%s-%s", chunk.ID, hashQuestion(newQ))
			indexInfoToUpdate = append(indexInfoToUpdate, &types.IndexInfo{
				Content:         buildContent(newQ, normalizedNewMeta.Answers),
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType:   types.KnowledgeTypeFAQ,
				TagID:           chunk.TagID,
				IsEnabled:       chunk.IsEnabled,
				IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
			})
			if !existedBefore {
				addedQuestions = append(addedQuestions, newQ)
			} else {
				updatedQuestions = append(updatedQuestions, newQ)
			}
		}
	}

	// 输出详细的变化日志
	if len(deletedQuestions) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: deleted similar questions: %v", deletedQuestions)
	}
	if len(addedQuestions) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: added similar questions: %v", addedQuestions)
	}
	if len(updatedQuestions) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: updated similar questions (answers changed): %v", updatedQuestions)
	}

	// 3. 删除不再存在的相似问索引
	if len(sourceIDsToDelete) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: deleting %d obsolete sourceIDs: %v", len(sourceIDsToDelete), sourceIDsToDelete)
		if delErr := retrieveEngine.DeleteBySourceIDList(ctx, sourceIDsToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); delErr != nil {
			logger.Warnf(ctx, "incrementalIndexFAQEntry: failed to delete obsolete source IDs: %v", delErr)
		}
	}

	// 4. 批量索引需要更新的内容
	newCount := len(normalizedNewMeta.SimilarQuestions)
	if len(indexInfoToUpdate) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: updating %d index entries (skipped %d unchanged)",
			len(indexInfoToUpdate), 1+newCount-len(indexInfoToUpdate))
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoToUpdate); err != nil {
			return err
		}
	} else {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: all %d entries unchanged, skipping index update", 1+newCount)
	}

	// 5. 更新 knowledge 记录
	now := time.Now()
	knowledge.UpdatedAt = now
	knowledge.ProcessedAt = &now
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return err
	}

	totalDuration := time.Since(indexStartTime)
	logger.Debugf(ctx, "incrementalIndexFAQEntry: completed in %v, updated %d/%d entries",
		totalDuration, len(indexInfoToUpdate), 1+newCount)

	return nil
}

func (s *knowledgeService) indexFAQChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge,
	chunks []*types.Chunk, embeddingModel embedding.Embedder,
	adjustStorage bool, needDelete bool,
) error {
	if len(chunks) == 0 {
		return nil
	}
	indexStartTime := time.Now()
	logger.Debugf(ctx, "indexFAQChunks: starting to index %d chunks", len(chunks))

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		return err
	}

	// 构建索引信息
	buildIndexInfoStartTime := time.Now()
	indexInfo := make([]*types.IndexInfo, 0)
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		infoList, err := s.buildFAQIndexInfoList(ctx, kb, chunk)
		if err != nil {
			return err
		}
		indexInfo = append(indexInfo, infoList...)
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	buildIndexInfoDuration := time.Since(buildIndexInfoStartTime)
	logger.Debugf(
		ctx,
		"indexFAQChunks: built %d index info entries for %d chunks in %v",
		len(indexInfo),
		len(chunks),
		buildIndexInfoDuration,
	)

	var size int64
	if adjustStorage {
		estimateStartTime := time.Now()
		size = retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfo)
		estimateDuration := time.Since(estimateStartTime)
		logger.Debugf(ctx, "indexFAQChunks: estimated storage size %d bytes in %v", size, estimateDuration)
		if tenantInfo.StorageQuota > 0 && tenantInfo.StorageUsed+size > tenantInfo.StorageQuota {
			return types.NewStorageQuotaExceededError()
		}
	}

	// 删除旧向量
	var deleteDuration time.Duration
	if needDelete {
		deleteStartTime := time.Now()
		if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
			logger.Warnf(ctx, "Delete FAQ vectors failed: %v", err)
		}
		deleteDuration = time.Since(deleteStartTime)
		if deleteDuration > 100*time.Millisecond {
			logger.Debugf(ctx, "indexFAQChunks: deleted old vectors for %d chunks in %v", len(chunkIDs), deleteDuration)
		}
	}

	// 批量索引（这里可能是性能瓶颈）
	batchIndexStartTime := time.Now()
	if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo); err != nil {
		return err
	}
	batchIndexDuration := time.Since(batchIndexStartTime)
	var avgPerEntry time.Duration
	if len(indexInfo) > 0 {
		avgPerEntry = batchIndexDuration / time.Duration(len(indexInfo))
	}
	logger.Debugf(ctx, "indexFAQChunks: batch indexed %d index info entries in %v (avg: %v per entry)",
		len(indexInfo), batchIndexDuration, avgPerEntry)

	if adjustStorage && size > 0 {
		adjustStartTime := time.Now()
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, size); err == nil {
			tenantInfo.StorageUsed += size
		}
		knowledge.StorageSize += size
		adjustDuration := time.Since(adjustStartTime)
		if adjustDuration > 50*time.Millisecond {
			logger.Debugf(ctx, "indexFAQChunks: adjusted storage in %v", adjustDuration)
		}
	}

	updateStartTime := time.Now()
	now := time.Now()
	knowledge.UpdatedAt = now
	knowledge.ProcessedAt = &now
	err = s.repo.UpdateKnowledge(ctx, knowledge)
	updateDuration := time.Since(updateStartTime)
	if updateDuration > 50*time.Millisecond {
		logger.Debugf(ctx, "indexFAQChunks: updated knowledge in %v", updateDuration)
	}

	totalDuration := time.Since(indexStartTime)
	logger.Debugf(
		ctx,
		"indexFAQChunks: completed indexing %d chunks in %v (build: %v, delete: %v, batchIndex: %v, update: %v)",
		len(chunks),
		totalDuration,
		buildIndexInfoDuration,
		deleteDuration,
		batchIndexDuration,
		updateDuration,
	)

	return err
}

func (s *knowledgeService) deleteFAQChunkVectors(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []*types.Chunk,
) error {
	if len(chunks) == 0 {
		return nil
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return err
	}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		return err
	}

	indexInfo := make([]*types.IndexInfo, 0)
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		infoList, err := s.buildFAQIndexInfoList(ctx, kb, chunk)
		if err != nil {
			return err
		}
		indexInfo = append(indexInfo, infoList...)
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	size := retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfo)
	if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
		return err
	}
	if size > 0 {
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -size); err == nil {
			tenantInfo.StorageUsed -= size
			if tenantInfo.StorageUsed < 0 {
				tenantInfo.StorageUsed = 0
			}
		}
		if knowledge.StorageSize >= size {
			knowledge.StorageSize -= size
		} else {
			knowledge.StorageSize = 0
		}
	}
	knowledge.UpdatedAt = time.Now()
	return s.repo.UpdateKnowledge(ctx, knowledge)
}

func faqImportCompletedOutcome(successCount, failedCount, skippedCount int) types.AuditOutcome {
	if successCount > 0 && (failedCount > 0 || skippedCount > 0) {
		return types.AuditOutcomePartial
	}
	if successCount > 0 {
		return types.AuditOutcomeSuccess
	}
	if failedCount > 0 && skippedCount > 0 {
		return types.AuditOutcomePartial
	}
	if failedCount > 0 || skippedCount > 0 {
		return types.AuditOutcomeFailed
	}
	return types.AuditOutcomeSuccess
}

func faqImportActivityDetails(payload *types.FAQImportPayload, progress *types.FAQImportProgress, totalEntries int) map[string]any {
	details := map[string]any{"mode": payload.Mode}
	if progress == nil {
		return details
	}
	total := totalEntries
	if total <= 0 {
		total = progress.Total
	}
	if total > 0 {
		details["total"] = total
	}
	details["count"] = progress.SuccessCount
	if progress.FailedCount > 0 {
		details["failed"] = progress.FailedCount
	}
	skipped := progress.SkippedCount
	if skipped <= 0 && total > 0 {
		skipped = total - progress.SuccessCount - progress.FailedCount
		if skipped < 0 {
			skipped = 0
		}
	}
	if skipped > 0 {
		details["skipped"] = skipped
	}
	return details
}

func (s *knowledgeService) recordFAQImportKBActivity(
	ctx context.Context,
	payload *types.FAQImportPayload,
	progress *types.FAQImportProgress,
	totalEntries int,
	action types.AuditAction,
	outcome types.AuditOutcome,
) {
	if s == nil || payload == nil || payload.DryRun || payload.KBID == "" {
		return
	}
	recordKBActivity(ctx, s.audit, payload.TenantID, payload.KBID, action,
		"faq_entry", payload.KnowledgeID, outcome, faqImportActivityDetails(payload, progress, totalEntries))
}

// ProcessFAQImport handles Asynq FAQ import tasks (including dry run mode)
func (s *knowledgeService) ProcessFAQImport(ctx context.Context, t *asynq.Task) error {
	var payload types.FAQImportPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal FAQ import task payload: %v", err)
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}
	ctx = payload.Initiator.Apply(ctx)
	ctx = withKBActivityTask(ctx, payload.TaskID, kbActivityTrigger(ctx))

	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "faq_import", payload.TaskID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// 获取任务重试信息，用于判断是否是最后一次重试
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// 如果 entries 存储在对象存储中，先下载
	if payload.EntriesURL != "" && len(payload.Entries) == 0 {
		logger.Infof(ctx, "Downloading FAQ entries from object storage: %s", payload.EntriesURL)
		reader, err := s.fileSvc.GetFile(ctx, payload.EntriesURL)
		if err != nil {
			logger.Errorf(ctx, "Failed to download FAQ entries from object storage: %v", err)
			return fmt.Errorf("failed to download entries: %w", err)
		}
		defer reader.Close()

		entriesData, err := io.ReadAll(reader)
		if err != nil {
			logger.Errorf(ctx, "Failed to read FAQ entries data: %v", err)
			return fmt.Errorf("failed to read entries data: %w", err)
		}

		var entries []types.FAQEntryPayload
		if err := json.Unmarshal(entriesData, &entries); err != nil {
			logger.Errorf(ctx, "Failed to unmarshal FAQ entries: %v", err)
			return fmt.Errorf("failed to unmarshal entries: %w", err)
		}

		payload.Entries = entries
		logger.Infof(ctx, "Downloaded %d FAQ entries from object storage", len(entries))
	}

	logger.Infof(ctx, "Processing FAQ import task: task_id=%s, kb_id=%s, total_entries=%d, dry_run=%v, retry=%d/%d",
		payload.TaskID, payload.KBID, len(payload.Entries), payload.DryRun, retryCount, maxRetry)

	// 保存原始总数量
	originalTotalEntries := len(payload.Entries)

	// 初始化进度
	// 检查是否已有验证结果（用于重试时跳过验证）
	// 注意：必须在保存新 progress 之前查询，否则会被覆盖
	existingProgress, _ := s.GetFAQImportProgress(ctx, payload.TaskID)

	progress := &types.FAQImportProgress{
		TaskID:         payload.TaskID,
		KBID:           payload.KBID,
		KnowledgeID:    payload.KnowledgeID,
		Status:         types.FAQImportStatusProcessing,
		Progress:       0,
		Total:          originalTotalEntries,
		Processed:      0,
		SuccessCount:   0,
		FailedCount:    0,
		FailedEntries:  make([]types.FAQFailedEntry, 0),
		SuccessEntries: make([]types.FAQSuccessEntry, 0),
		Message:        "正在验证条目...",
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
		DryRun:         payload.DryRun,
	}
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to save initial FAQ import progress: %v", err)
	}

	var validEntryIndices []int
	if existingProgress != nil && len(existingProgress.ValidEntryIndices) > 0 {
		// 重试时直接使用之前的验证结果
		validEntryIndices = existingProgress.ValidEntryIndices
		progress.FailedCount = existingProgress.FailedCount
		progress.FailedEntries = existingProgress.FailedEntries
		logger.Infof(ctx, "Reusing previous validation result: valid=%d, failed=%d",
			len(validEntryIndices), progress.FailedCount)
	} else {
		// 第一步：执行验证（无论是 dry run 还是 import 模式都需要验证）
		validEntryIndices = s.executeFAQDryRunValidation(ctx, &payload, progress)
		// 保存验证通过的索引，用于重试时跳过验证
		progress.ValidEntryIndices = validEntryIndices
		if err := s.saveFAQImportProgress(ctx, progress); err != nil {
			logger.Warnf(ctx, "Failed to save validation result: %v", err)
		}
		logger.Infof(ctx, "FAQ validation completed: total=%d, valid=%d, failed=%d",
			originalTotalEntries, len(validEntryIndices), progress.FailedCount)
	}

	// Dry run 模式：验证完成后直接返回结果
	if payload.DryRun {
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// Import 模式：检查是否有有效条目需要导入
	if len(validEntryIndices) == 0 {
		// 没有有效条目，直接完成
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// 提取有效的条目
	validEntries := make([]types.FAQEntryPayload, 0, len(validEntryIndices))
	for _, idx := range validEntryIndices {
		validEntries = append(validEntries, payload.Entries[idx])
	}

	// 更新进度消息
	progress.Message = fmt.Sprintf("验证完成，开始导入 %d 条有效数据...", len(validEntries))
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to update FAQ import progress: %v", err)
	}

	// 幂等性检查：获取knowledge记录（FAQ任务使用knowledge ID作为taskID）
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get FAQ knowledge: %v", err)
		return nil
	}

	if knowledge == nil {
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KBID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt, types.FAQImportStatusFailed, 0, originalTotalEntries, 0, "获取知识库失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
			s.recordFAQImportKBActivity(ctx, &payload, progress, originalTotalEntries, types.AuditActionFAQImportFailed, types.AuditOutcomeFailed)
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("failed to get knowledge base: %w", err)
	}

	// 检查任务状态 - 幂等性处理（复用之前获取的 existingProgress）
	var processedCount int
	if existingProgress != nil {
		if existingProgress.Status == types.FAQImportStatusCompleted {
			logger.Infof(ctx, "FAQ import already completed, skipping: %s", payload.TaskID)
			if clearErr := s.clearRunningFAQImportInfoIfMatches(ctx, payload.KBID, payload.TaskID, payload.InstanceID, payload.EnqueuedAt); clearErr != nil {
				logger.Warnf(ctx, "Failed to clear running FAQ import info for completed task: %v", clearErr)
			}
			return nil // 幂等：已完成的任务直接返回
		}
		// 获取已处理的数量（注意：这是相对于 validEntries 的索引）
		processedCount = existingProgress.Processed - progress.FailedCount // 已处理数 - 验证失败数 = 已导入的有效条目数
		if processedCount < 0 {
			processedCount = 0
		}
		logger.Infof(ctx, "Resuming FAQ import from progress: %d/%d (valid entries)", processedCount, len(validEntries))
	}

	// 幂等性处理：清理可能已部分处理的chunks和索引数据
	chunksDeleted, err := s.chunkRepo.DeleteUnindexedChunks(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to delete unindexed chunks: %v", err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt, types.FAQImportStatusFailed, 0, originalTotalEntries, 0, "清理未索引数据失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
			s.recordFAQImportKBActivity(ctx, &payload, progress, originalTotalEntries, types.AuditActionFAQImportFailed, types.AuditOutcomeFailed)
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("failed to delete unindexed chunks: %w", err)
	}
	if len(chunksDeleted) > 0 {
		logger.Infof(ctx, "Deleted unindexed chunks: %d", len(chunksDeleted))

		// 删除索引数据
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err == nil {
			retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
				ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
			if err == nil {
				chunkIDs := make([]string, 0, len(chunksDeleted))
				for _, chunk := range chunksDeleted {
					chunkIDs = append(chunkIDs, chunk.ID)
				}
				if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
					logger.Warnf(ctx, "Failed to delete index data for chunks (may not exist): %v", err)
				} else {
					logger.Infof(ctx, "Successfully deleted index data for %d chunks", len(chunksDeleted))
				}
			}
		}
	}

	// 如果已经处理了一部分有效条目，从该位置继续
	entriesToImport := validEntries
	importMode := payload.Mode
	if processedCount > 0 && processedCount < len(validEntries) {
		entriesToImport = validEntries[processedCount:]
		// 重试场景下，如果之前已经处理了一部分数据，需要切换到 Append 模式
		// 因为 Replace 模式的删除操作在第一次运行时已经执行过了
		// 如果继续使用 Replace 模式，calculateReplaceOperations 会将之前成功导入的数据标记为删除
		// 导致数据丢失
		if payload.Mode == types.FAQBatchModeReplace {
			importMode = types.FAQBatchModeAppend
			logger.Infof(ctx, "Switching to Append mode for retry, original mode was Replace")
		}
		logger.Infof(ctx, "Continuing FAQ import from entry %d, remaining: %d entries", processedCount, len(entriesToImport))
	}

	// 构建FAQBatchUpsertPayload（使用验证通过的有效条目）
	faqPayload := &types.FAQBatchUpsertPayload{
		Entries: entriesToImport,
		Mode:    importMode,
	}

	// 执行FAQ导入（传入已处理的偏移量，用于进度计算）
	if err := s.executeFAQImport(ctx, payload.TaskID, payload.KBID, faqPayload, payload.TenantID, progress.FailedCount+processedCount, progress); err != nil {
		logger.Errorf(ctx, "FAQ import task failed: %s, error: %v", payload.TaskID, err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt, types.FAQImportStatusFailed, 0, originalTotalEntries, len(validEntries), "导入失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
			s.recordFAQImportKBActivity(ctx, &payload, progress, originalTotalEntries, types.AuditActionFAQImportFailed, types.AuditOutcomeFailed)
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("FAQ import failed: %w", err)
	}

	// 任务成功完成
	logger.Infof(ctx, "FAQ import task completed: %s, imported: %d, failed: %d",
		payload.TaskID, len(progress.SuccessEntries), progress.FailedCount)

	// 最终完成处理（生成失败条目 CSV 等）
	return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
}

// finalizeFAQValidation 完成 FAQ 验证/导入任务，生成失败条目 CSV（如果有）
func (s *knowledgeService) finalizeFAQValidation(ctx context.Context, payload *types.FAQImportPayload,
	progress *types.FAQImportProgress, originalTotalEntries int,
) error {
	// 清理对象存储中的 entries 文件（如果有）
	if payload.EntriesURL != "" {
		if err := s.fileSvc.DeleteFile(ctx, payload.EntriesURL); err != nil {
			logger.Warnf(ctx, "Failed to delete FAQ entries file from object storage: %v", err)
		} else {
			logger.Infof(ctx, "Deleted FAQ entries file from object storage: %s", payload.EntriesURL)
		}
	}
	progress.UpdatedAt = time.Now().Unix()

	// 如果有失败条目，生成 CSV 文件
	if len(progress.FailedEntries) > 0 {
		csvURL, err := s.generateFailedEntriesCSV(ctx, payload.TenantID, payload.TaskID, progress.FailedEntries)
		if err != nil {
			logger.Warnf(ctx, "Failed to generate failed entries CSV: %v", err)
		} else {
			progress.FailedEntriesURL = csvURL
			progress.FailedEntries = nil // 清空内联数据，使用 URL
			progress.Message += " (失败记录已导出为CSV)"
		}
	}

	// 必须在 saveFAQImportResultToDatabase 之前计算最终统计。
	progress.Status = types.FAQImportStatusCompleted
	progress.Progress = 100
	progress.Processed = originalTotalEntries

	if len(progress.ValidEntryIndices) > 0 {
		progress.SuccessCount = len(progress.ValidEntryIndices) - progress.PartialFailedCount
	} else if len(progress.SuccessEntries) > 0 {
		progress.SuccessCount = len(progress.SuccessEntries) - progress.PartialFailedCount
	} else {
		progress.SuccessCount = originalTotalEntries - progress.FailedCount - progress.PartialFailedCount
	}
	if progress.SuccessCount < 0 {
		progress.SuccessCount = 0
	}

	if progress.AddedCount == 0 && progress.MergedCount > 0 {
		progress.AddedCount = progress.SuccessCount - progress.MergedCount
		if progress.AddedCount < 0 {
			progress.AddedCount = 0
		}
	} else if progress.AddedCount == 0 {
		progress.AddedCount = progress.SuccessCount
	}

	skippedCount := originalTotalEntries - progress.SuccessCount - progress.PartialFailedCount - progress.FailedCount
	if skippedCount < 0 {
		skippedCount = 0
	}
	progress.SkippedCount = skippedCount

	if payload.DryRun {
		progress.Message = s.buildFAQImportResultMessage("验证完成", progress)
	} else {
		progress.Message = s.buildFAQImportResultMessage("导入完成", progress)
	}

	// 如果不是 dry run 模式，保存导入结果统计到数据库
	if !payload.DryRun {
		if err := s.saveFAQImportResultToDatabase(ctx, payload, progress, originalTotalEntries); err != nil {
			logger.Warnf(ctx, "Failed to save FAQ import result to database: %v", err)
		}

		// 只有 replace 模式才清理未使用的 Tag
		// append 模式不应删除用户预先创建的空标签
		if payload.Mode == types.FAQBatchModeReplace {
			deletedTags, err := s.tagRepo.DeleteUnusedTags(ctx, payload.TenantID, payload.KBID)
			if err != nil {
				logger.Warnf(ctx, "FAQ import task %s: failed to cleanup unused tags: %v", payload.TaskID, err)
			} else if deletedTags > 0 {
				logger.Infof(ctx, "FAQ import task %s: cleaned up %d unused tags after replace import", payload.TaskID, deletedTags)
			}
		}
	}

	// 使用 updateFAQImportProgressStatus 来确保正确清理 running key
	// 但是需要先保存其他字段，因为 updateFAQImportProgressStatus 不会保存所有字段
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to save final FAQ import progress: %v", err)
	}

	// 然后调用状态更新来清理 running key
	if err := s.updateFAQImportProgressStatus(ctx, payload.TaskID, payload.InstanceID, payload.EnqueuedAt, types.FAQImportStatusCompleted,
		100, originalTotalEntries, originalTotalEntries, progress.Message, ""); err != nil {
		logger.Warnf(ctx, "Failed to update final FAQ import status: %v", err)
	}

	logger.Infof(ctx, "FAQ task completed: %s, dry_run=%v, success: %d, added: %d, merged: %d, failed: %d, partial_failed: %d",
		payload.TaskID, payload.DryRun, progress.SuccessCount, progress.AddedCount, progress.MergedCount, progress.FailedCount, progress.PartialFailedCount)

	if !payload.DryRun {
		outcome := faqImportCompletedOutcome(progress.SuccessCount, progress.FailedCount, progress.SkippedCount)
		s.recordFAQImportKBActivity(ctx, payload, progress, originalTotalEntries,
			types.AuditActionFAQImportCompleted, outcome)
	}

	return nil
}

// executeFAQMergeOperations 批量执行 append-mode 合并操作：更新已有 chunk
// 的 metadata / content / 索引。使用 ListChunksByID 批量加载 + SaveChunks 事
// 务批量保存，减少 DB 往返。任一批次失败立即返回，由 executeFAQImport
// 的 defer recovery 决定是否回滚整次导入。
//
// 必要时这里逐条 fan-out 索引（EFPutDocument），而不是合并 chunks 批量
// 重建：因为索引底层是按 SourceID 覆盖写入，按合并后的最终内容直接 put
// 即可。
func (s *knowledgeService) executeFAQMergeOperations(
	ctx context.Context,
	taskID string,
	kb *types.KnowledgeBase,
	faqKnowledge *types.Knowledge,
	embeddingModel embedding.Embedder,
	indexMode types.FAQIndexMode,
	mergeOps []faqMergeOperation,
	progress *types.FAQImportProgress,
) (int, error) {
	if len(mergeOps) == 0 {
		return 0, nil
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	mergedCount := 0

	for batchStart := 0; batchStart < len(mergeOps); batchStart += faqImportBatchSize {
		batchEnd := batchStart + faqImportBatchSize
		if batchEnd > len(mergeOps) {
			batchEnd = len(mergeOps)
		}
		batch := mergeOps[batchStart:batchEnd]

		// 1. 批量加载完整 chunk（calculateAppendOperations 中只加载了部分字段，
		//    缺少 status/is_enabled/flags/seq_id 等，直接更新会将这些字段覆盖为零值）
		chunkIDs := make([]string, len(batch))
		for i, op := range batch {
			chunkIDs[i] = op.ExistingChunk.ID
		}
		fullChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
		if err != nil {
			logger.Errorf(ctx, "FAQ import task %s: failed to batch reload chunks for merge: %v", taskID, err)
			return mergedCount, fmt.Errorf("failed to batch reload chunks for merge: %w", err)
		}
		chunkMap := make(map[string]*types.Chunk, len(fullChunks))
		for _, c := range fullChunks {
			chunkMap[c.ID] = c
		}

		// 2. 逐条应用合并数据
		mergedChunks := make([]*types.Chunk, 0, len(batch))
		for _, op := range batch {
			fullChunk, ok := chunkMap[op.ExistingChunk.ID]
			if !ok {
				logger.Errorf(ctx, "FAQ import task %s: chunk %s not found during batch reload", taskID, op.ExistingChunk.ID)
				return mergedCount, fmt.Errorf("chunk %s not found during batch reload", op.ExistingChunk.ID)
			}

			if err := fullChunk.SetFAQMetadata(op.MergedMeta); err != nil {
				logger.Errorf(ctx, "FAQ import task %s: failed to set merged metadata for chunk %s: %v", taskID, fullChunk.ID, err)
				return mergedCount, fmt.Errorf("failed to set merged FAQ metadata: %w", err)
			}

			fullChunk.Content = buildFAQChunkContent(op.MergedMeta, indexMode)
			fullChunk.ContentHash = types.CalculateFAQContentHash(op.MergedMeta)
			fullChunk.UpdatedAt = time.Now()

			// 用新值覆盖运营状态
			if op.Entry.IsEnabled != nil {
				fullChunk.IsEnabled = *op.Entry.IsEnabled
			}
			if op.Entry.IsRecommended != nil {
				if *op.Entry.IsRecommended {
					fullChunk.Flags = fullChunk.Flags.SetFlag(types.ChunkFlagRecommended)
				} else {
					fullChunk.Flags = fullChunk.Flags.ClearFlag(types.ChunkFlagRecommended)
				}
			}

			mergedChunks = append(mergedChunks, fullChunk)
		}

		// 3. 事务批量保存（GORM Save 全字段更新，确保 metadata/content_hash 持久化）
		if err := s.chunkRepo.SaveChunks(ctx, mergedChunks); err != nil {
			logger.Errorf(ctx, "FAQ import task %s: failed to batch save merged chunks: %v", taskID, err)
			return mergedCount, fmt.Errorf("failed to batch save merged chunks: %w", err)
		}

		// 4. 重建索引（EFPutDocument 会自动覆盖相同 SourceID）
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, mergedChunks, embeddingModel, false, false); err != nil {
			return mergedCount, fmt.Errorf("failed to re-index merged chunks: %w", err)
		}

		// 5. 收集成功条目信息
		for i, op := range batch {
			chunk := mergedChunks[i]
			meta := op.MergedMeta
			var tagID int64
			tagName := ""
			if chunk.TagID != "" {
				if tag, tErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID); tErr == nil && tag != nil {
					tagID = tag.SeqID
					tagName = tag.Name
				}
			}
			progress.SuccessEntries = append(progress.SuccessEntries, types.FAQSuccessEntry{
				Index:            op.Detail.Index,
				SeqID:            chunk.SeqID,
				TagID:            tagID,
				TagName:          tagName,
				StandardQuestion: meta.StandardQuestion,
			})
		}

		mergedCount += len(batch)

		logger.Infof(ctx, "FAQ import task %s: merged batch %d-%d (%d chunks)", taskID, batchStart+1, batchEnd, len(mergedChunks))
	}

	return mergedCount, nil
}

// buildFAQImportResultMessage 构建 FAQ 导入 / 验证最终结果的人类可读消息。
// 前端会把它直接展示在 toast / 任务列表里，所以保持简单清晰：
//   - 默认形态："导入完成 / 上传 N 条 / 成功 X 条 [/ 失败 Y 条] [/ 部分失败 Z 条]"
//   - 当 MergedCount > 0 时改用拆分形态："/ 新增 X 条 / 合并更新 Y 条"，
//     让用户在 append 模式下看到合并了多少条历史 FAQ 而不是只看到总成功数。
//
// 内部 master 原始实现；HEAD 版本之前没有，所有完成消息只有 "正在处理第 N/M 条"。
func (s *knowledgeService) buildFAQImportResultMessage(prefix string, progress *types.FAQImportProgress) string {
	parts := []string{prefix}
	parts = append(parts, fmt.Sprintf("上传 %d 条", progress.Total))

	if progress.MergedCount > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 条", progress.AddedCount))
		parts = append(parts, fmt.Sprintf("合并更新 %d 条", progress.MergedCount))
	} else {
		parts = append(parts, fmt.Sprintf("成功 %d 条", progress.SuccessCount))
	}

	if progress.FailedCount > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 条", progress.FailedCount))
	}
	if progress.PartialFailedCount > 0 {
		parts = append(parts, fmt.Sprintf("部分失败 %d 条", progress.PartialFailedCount))
	}

	return strings.Join(parts, " / ")
}

const (
	faqImportProgressKeyPrefix = "faq_import_progress:"
	faqImportRunningKeyPrefix  = "faq_import_running:"
	faqImportProgressTTL       = 3 * time.Hour
)

// getFAQImportProgressKey returns the Redis key for storing FAQ import progress
func getFAQImportProgressKey(taskID string) string {
	return faqImportProgressKeyPrefix + taskID
}

// getFAQImportRunningKey returns the Redis key for storing running task ID by KB ID
func getFAQImportRunningKey(kbID string) string {
	return faqImportRunningKeyPrefix + kbID
}

// saveFAQImportProgress saves the FAQ import progress to Redis
func (s *knowledgeService) saveFAQImportProgress(ctx context.Context, progress *types.FAQImportProgress) error {
	if s.redisClient == nil {
		progress.UpdatedAt = time.Now().Unix()
		s.memFAQProgress.Store(progress.TaskID, progress)
		return nil
	}
	key := getFAQImportProgressKey(progress.TaskID)
	progress.UpdatedAt = time.Now().Unix()
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal FAQ import progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, faqImportProgressTTL).Err()
}

// GetFAQImportProgress retrieves the progress of an FAQ import task
func (s *knowledgeService) GetFAQImportProgress(ctx context.Context, taskID string) (*types.FAQImportProgress, error) {
	if s.redisClient == nil {
		if v, ok := s.memFAQProgress.Load(taskID); ok {
			return v.(*types.FAQImportProgress), nil
		}
		return nil, werrors.NewNotFoundError("FAQ import task not found")
	}
	key := getFAQImportProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("FAQ import task not found")
		}
		return nil, fmt.Errorf("failed to get FAQ import progress from Redis: %w", err)
	}

	var progress types.FAQImportProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal FAQ import progress: %w", err)
	}

	// If task is completed, enrich with persisted result fields from database
	if progress.Status == types.FAQImportStatusCompleted && progress.KnowledgeID != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
		knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, progress.KnowledgeID)
		if err == nil && knowledge != nil {
			if result, err := knowledge.GetLastFAQImportResult(); err == nil && result != nil {
				progress.SuccessCount = result.SuccessCount
				progress.FailedCount = result.FailedCount
				progress.PartialFailedCount = result.PartialFailedCount
				progress.SkippedCount = result.SkippedCount
				progress.MergedCount = result.MergedCount
				progress.AddedCount = result.AddedCount
				progress.ImportMode = result.ImportMode
				progress.ImportedAt = result.ImportedAt
				progress.DisplayStatus = result.DisplayStatus
				progress.ProcessingTime = result.ProcessingTime
				if result.FailedEntriesURL != "" {
					progress.FailedEntriesURL = result.FailedEntriesURL
				}
			}
		}
	}

	return &progress, nil
}

// UpdateLastFAQImportResultDisplayStatus updates the display status of FAQ import result
func (s *knowledgeService) UpdateLastFAQImportResultDisplayStatus(ctx context.Context, kbID string, displayStatus string) error {
	// 验证displayStatus参数
	if displayStatus != "open" && displayStatus != "close" {
		return werrors.NewBadRequestError("invalid display status, must be 'open' or 'close'")
	}

	// 获取当前空间ID
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 查找FAQ类型的knowledge
	knowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return fmt.Errorf("failed to list knowledge: %w", err)
	}

	// 查找FAQ类型的knowledge
	var faqKnowledge *types.Knowledge
	for _, k := range knowledgeList {
		if k.Type == types.KnowledgeTypeFAQ {
			faqKnowledge = k
			break
		}
	}

	if faqKnowledge == nil {
		return werrors.NewNotFoundError("FAQ knowledge not found in this knowledge base")
	}

	// 解析当前的导入结果
	result, err := faqKnowledge.GetLastFAQImportResult()
	if err != nil {
		return fmt.Errorf("failed to parse FAQ import result: %w", err)
	}

	if result == nil {
		return werrors.NewNotFoundError("no FAQ import result found")
	}

	// 更新显示状态
	result.DisplayStatus = displayStatus

	// 保存更新后的结果
	if err := faqKnowledge.SetLastFAQImportResult(result); err != nil {
		return fmt.Errorf("failed to set FAQ import result: %w", err)
	}

	// 更新数据库
	if err := s.repo.UpdateKnowledge(ctx, faqKnowledge); err != nil {
		return fmt.Errorf("failed to update knowledge: %w", err)
	}

	return nil
}
