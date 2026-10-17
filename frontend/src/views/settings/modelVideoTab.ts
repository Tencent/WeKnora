/**
 * The model settings page's 「视频」 tab is a parent view: it lists the models
 * whose catalog entry declares video input. It is deliberately not a model
 * type — a model is still a chat/VLM/embedding model that happens to accept
 * video — so this tab never creates models and editing a card keeps the real
 * type.
 *
 * The join is done by the server: GET /api/v1/models returns
 * `capabilities.input`, which `internal/handler/dto` fills from
 * `modelruntime.Resolve` (provider + model name + catalog overrides). Re-joining
 * by name on the client would have to guess which provider the name belongs to,
 * so the resolved capabilities are the single source of truth.
 */
export interface VideoCapableModel {
  capabilities?: { input?: string[] | null } | null
}

export function supportsVideoInput(model: VideoCapableModel): boolean {
  return Array.isArray(model.capabilities?.input)
    && model.capabilities!.input!.includes('video')
}

export function countVideoCapable(models: VideoCapableModel[]): number {
  return models.filter(supportsVideoInput).length
}
