-- 阶段 0：将没有任何处理任务的历史 uploading 记录标记为失败。
-- 正常上传会在 multipart/complete 后创建 thumbnail 任务，因此这些记录
-- 无法继续推进，保留在 uploading 会让它们永久成为列表不可见的孤儿。
UPDATE videos
SET
    status = 'failed',
    processing_error_summary = COALESCE(
        NULLIF(processing_error_summary, ''),
        'stage0: orphan uploading record without processing task'
    ),
    updated_at = CURRENT_TIMESTAMP
WHERE status = 'uploading'
  AND NOT EXISTS (
      SELECT 1
      FROM video_processing_jobs
      WHERE video_processing_jobs.video_id = videos.id
  );
