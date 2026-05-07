package doris

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Stream Load 相关常量。
const (
	// 单批 Stream Load 的 JSON body 上限（保守值，远小于 Doris 默认 streaming_load_max_mb=10240）。
	// 主要目的是控制单次 HTTP 请求的尾延迟，超过则自动拆批。
	streamLoadMaxBatchBytes = 1 << 20 // 1 MiB

	// HTTP 头 Authorization 用 Basic auth；Stream Load 也支持 token，
	// 这里走最常用的用户名/密码方案与 MySQL 协议保持一致。
	headerAuthorization = "Authorization"
	headerExpect        = "Expect"
	headerContentType   = "Content-Type"
)

// streamLoadResponse 是 Doris FE/BE 返回的 Stream Load 结果体。
//
// 关键字段：Status 应为 "Success" 或 "Publish Timeout"（后者表示数据已写入但发布事务超时，
// 仍视为成功）。其它状态都视为失败。
type streamLoadResponse struct {
	TxnId                  int64  `json:"TxnId"`
	Label                  string `json:"Label"`
	Status                 string `json:"Status"`
	Message                string `json:"Message"`
	NumberTotalRows        int64  `json:"NumberTotalRows"`
	NumberLoadedRows       int64  `json:"NumberLoadedRows"`
	NumberFilteredRows     int64  `json:"NumberFilteredRows"`
	NumberUnselectedRows   int64  `json:"NumberUnselectedRows"`
	LoadBytes              int64  `json:"LoadBytes"`
	LoadTimeMs             int64  `json:"LoadTimeMs"`
	BeginTxnTimeMs         int64  `json:"BeginTxnTimeMs"`
	StreamLoadPutTimeMs    int64  `json:"StreamLoadPutTimeMs"`
	ReadDataTimeMs         int64  `json:"ReadDataTimeMs"`
	WriteDataTimeMs        int64  `json:"WriteDataTimeMs"`
	CommitAndPublishTimeMs int64  `json:"CommitAndPublishTimeMs"`
	ErrorURL               string `json:"ErrorURL"`
}

// streamLoadURL 拼装某张表的 Stream Load HTTP 端点。
func (r *dorisRepository) streamLoadURL(table string) string {
	return fmt.Sprintf("%s/api/%s/%s/_stream_load",
		r.feHTTPBase, r.database, table)
}

// partialUpdateRows 把若干行通过 Stream Load 的 partial update 模式写回目标表。
//
// columns 是参与本次 partial update 的列（必须包含 UNIQUE KEY 列，即 "id"）。
// rows 中每一项是一个与 columns 等长的字段值数组。
//
// 实现要点：
//  1. 用 JSON 数组的 body 形式，header 加 strip_outer_array=true。
//  2. 设置 partial_columns=true、merge_type=APPEND，触发 Doris 的 partial update 模式
//     (Doris 4.1 + UNIQUE KEY MoW 表的标准玩法)。
//  3. 按 streamLoadMaxBatchBytes 自动拆批，避免单次过大。
//  4. 处理 307：Doris 的 FE 会 redirect 到 BE，net/http 默认会跟随；
//     这里需要保证 GetBody 可重发（已通过 bytes.NewReader 构造 Body 满足）。
func (r *dorisRepository) partialUpdateRows(ctx context.Context,
	table string, columns []string, rows []map[string]any,
) error {
	if len(rows) == 0 {
		return nil
	}
	for _, batch := range chunkRows(rows, streamLoadMaxBatchBytes) {
		if err := r.streamLoadOnce(ctx, table, columns, batch); err != nil {
			return err
		}
	}
	return nil
}

// streamLoadOnce 发出一次 Stream Load HTTP 请求。
func (r *dorisRepository) streamLoadOnce(ctx context.Context,
	table string, columns []string, rows []map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(rows) == 0 {
		return nil
	}

	body, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal stream load body: %w", err)
	}

	url := r.streamLoadURL(table)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build stream load request: %w", err)
	}

	// GetBody 让 redirect 时可以重新读 body（FE -> BE 的 307 需要重发 PUT body）。
	bodyCopy := append([]byte(nil), body...)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
	req.ContentLength = int64(len(body))

	auth := base64.StdEncoding.EncodeToString([]byte(r.username + ":" + r.password))
	req.Header.Set(headerAuthorization, "Basic "+auth)
	req.Header.Set(headerExpect, "100-continue")
	req.Header.Set(headerContentType, "application/json")
	req.Header.Set("format", "json")
	req.Header.Set("strip_outer_array", "true")
	req.Header.Set("partial_columns", "true")
	req.Header.Set("columns", strings.Join(columns, ","))
	req.Header.Set("merge_type", "APPEND")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stream load HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read stream load response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("stream load HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result streamLoadResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode stream load response: %w (raw=%s)", err, string(respBody))
	}

	switch result.Status {
	case "Success", "Publish Timeout":
		log.Infof("[Doris] Stream load %s OK: rows=%d, loaded=%d, label=%s",
			table, result.NumberTotalRows, result.NumberLoadedRows, result.Label)
		return nil
	default:
		return fmt.Errorf("stream load failed: status=%s msg=%s err_url=%s",
			result.Status, result.Message, result.ErrorURL)
	}
}

// chunkRows 把行按累积 JSON 体大小切分，每段不超过 maxBytes。
//
// 注意：JSON 序列化的实际开销大约是 marshal 后的字节数，而单行 marshal
// 加上逗号 + 数组括号约等于本估算。这里用粗略估计避免每段都 marshal。
func chunkRows(rows []map[string]any, maxBytes int) [][]map[string]any {
	if len(rows) == 0 {
		return nil
	}

	var (
		out    [][]map[string]any
		curr   []map[string]any
		size   int
		header = 2 // "[" + "]"
	)

	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			// marshal 失败时把这一行单独成段，由上层 streamLoadOnce 再次 marshal 报错。
			if len(curr) > 0 {
				out = append(out, curr)
			}
			out = append(out, []map[string]any{row})
			curr = nil
			size = 0
			continue
		}
		// 加上逗号位（除第一行之外）。
		need := len(raw)
		if len(curr) > 0 {
			need++
		}
		if size+need+header > maxBytes && len(curr) > 0 {
			out = append(out, curr)
			curr = nil
			size = 0
		}
		curr = append(curr, row)
		size += need
	}
	if len(curr) > 0 {
		out = append(out, curr)
	}
	return out
}

// ---------------------------------------------------------------------------
// 业务面方法：BatchUpdateChunkEnabledStatus / BatchUpdateChunkTagID
// ---------------------------------------------------------------------------

// BatchUpdateChunkEnabledStatus 批量更新 chunk 的 is_enabled 字段。
//
// 由于 Stream Load partial update 需要主键列 id，但 chunk_id 不一定等于 id，
// 这里做一个查表步骤：通过 chunk_id IN (...) 查出对应的 (id, table) 列表，
// 再分表用 partial update 写回。
//
// 时间复杂度上是 O(N)：扫描一遍 chunk_id IN (...) 在 INVERTED 索引下走点查。
func (r *dorisRepository) BatchUpdateChunkEnabledStatus(ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}

	chunkIDs := make([]string, 0, len(chunkStatusMap))
	for id := range chunkStatusMap {
		chunkIDs = append(chunkIDs, id)
	}

	mapping, err := r.lookupChunkRowKeys(ctx, chunkIDs)
	if err != nil {
		return err
	}

	// 按表分组 -> 每行 (id, is_enabled)。
	byTable := make(map[string][]map[string]any)
	for chunkID, locations := range mapping {
		enabled, ok := chunkStatusMap[chunkID]
		if !ok {
			continue
		}
		for _, loc := range locations {
			byTable[loc.table] = append(byTable[loc.table], map[string]any{
				fieldID:        loc.id,
				fieldIsEnabled: enabled,
			})
		}
	}
	for table, rows := range byTable {
		if err := r.partialUpdateRows(ctx, table, []string{fieldID, fieldIsEnabled}, rows); err != nil {
			return fmt.Errorf("partial update is_enabled in %s: %w", table, err)
		}
	}
	return nil
}

// BatchUpdateChunkTagID 批量更新 chunk 的 tag_id 字段。逻辑与 EnabledStatus 一致。
func (r *dorisRepository) BatchUpdateChunkTagID(ctx context.Context,
	chunkTagMap map[string]string,
) error {
	if len(chunkTagMap) == 0 {
		return nil
	}

	chunkIDs := make([]string, 0, len(chunkTagMap))
	for id := range chunkTagMap {
		chunkIDs = append(chunkIDs, id)
	}

	mapping, err := r.lookupChunkRowKeys(ctx, chunkIDs)
	if err != nil {
		return err
	}

	byTable := make(map[string][]map[string]any)
	for chunkID, locations := range mapping {
		tagID, ok := chunkTagMap[chunkID]
		if !ok {
			continue
		}
		for _, loc := range locations {
			byTable[loc.table] = append(byTable[loc.table], map[string]any{
				fieldID:    loc.id,
				fieldTagID: tagID,
			})
		}
	}
	for table, rows := range byTable {
		if err := r.partialUpdateRows(ctx, table, []string{fieldID, fieldTagID}, rows); err != nil {
			return fmt.Errorf("partial update tag_id in %s: %w", table, err)
		}
	}
	return nil
}

// rowLocation 表示某行在哪个表里、主键 id 是什么。
type rowLocation struct {
	table string
	id    string
}

// lookupChunkRowKeys 查询给定的 chunkIDs 在所有 <base>_<dim> 表中的物理位置：
//   - key：chunk_id
//   - value：[(table, id), ...]，因为同一 chunk 可能在多个维度的表里都有副本。
//
// 跨表查询使用 listEmbeddingTables 列出的所有匹配表；每张表执行一次
// SELECT id, chunk_id FROM <table> WHERE chunk_id IN (?, ?, ...)。
func (r *dorisRepository) lookupChunkRowKeys(ctx context.Context,
	chunkIDs []string,
) (map[string][]rowLocation, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	if len(tables) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(chunkIDs))
	args := make([]any, len(chunkIDs))
	for i, v := range chunkIDs {
		placeholders[i] = "?"
		args[i] = v
	}

	out := make(map[string][]rowLocation)
	for _, table := range tables {
		stmt := fmt.Sprintf(
			"SELECT %s, %s FROM `%s` WHERE %s IN (%s)",
			fieldID, fieldChunkID, table, fieldChunkID, strings.Join(placeholders, ", "),
		)
		rows, err := r.db.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, fmt.Errorf("lookup chunk row keys in %s: %w", table, err)
		}
		for rows.Next() {
			var id, chunkID string
			if err := rows.Scan(&id, &chunkID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan row keys: %w", err)
			}
			out[chunkID] = append(out[chunkID], rowLocation{table: table, id: id})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
