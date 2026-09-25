/**
 * 将 API 返回的知识库范围归一化为前端表单可安全使用的数组。
 *
 * @param ids API Key 的知识库 ID；完全授权的 Key 可能由服务端返回 null。
 * @returns 一份新的知识库 ID 数组；null 或 undefined 返回空数组，表示全部知识库。
 */
export function normalizeAPIKeyKnowledgeBaseIDs(
  ids: readonly string[] | null | undefined,
): string[] {
  return ids ? [...ids] : []
}
