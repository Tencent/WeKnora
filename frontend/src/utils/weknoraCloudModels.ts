import type { ModelConfig } from '@/api/model'

export const WEKNORA_CLOUD_BASE_URL = 'https://weknora.weixin.qq.com'
export const WEKNORA_CLOUD_PROVIDER = 'weknoracloud'

export type WkcModelKind = 'chat' | 'embedding' | 'rerank' | 'vllm'

const BACKEND_TYPE_BY_KIND: Record<WkcModelKind, ModelConfig['type']> = {
  chat: 'KnowledgeQA',
  embedding: 'Embedding',
  rerank: 'Rerank',
  vllm: 'VLLM',
}

export const WKC_MODEL_NAME_BY_KIND: Record<WkcModelKind, string> = {
  chat: 'chat',
  embedding: 'embedding',
  rerank: 'rerank',
  vllm: 'vlm',
}

export const WKC_MODEL_KINDS: WkcModelKind[] = ['chat', 'embedding', 'rerank', 'vllm']

export function isWeKnoraCloudModel(model: ModelConfig): boolean {
  return model.parameters?.provider === WEKNORA_CLOUD_PROVIDER
}

/** Returns kinds that already have a WeKnoraCloud model of the matching backend type. */
export function existingWkcKinds(models: ModelConfig[]): Set<WkcModelKind> {
  const found = new Set<WkcModelKind>()
  for (const model of models) {
    if (!isWeKnoraCloudModel(model)) continue
    for (const kind of WKC_MODEL_KINDS) {
      if (model.type === BACKEND_TYPE_BY_KIND[kind]) {
        found.add(kind)
      }
    }
  }
  return found
}

export function buildWkcModelConfig(
  kind: WkcModelKind,
  displayName: string,
  dimension?: number,
): ModelConfig {
  const name = WKC_MODEL_NAME_BY_KIND[kind]
  const type = BACKEND_TYPE_BY_KIND[kind]
  const parameters: ModelConfig['parameters'] = {
    base_url: WEKNORA_CLOUD_BASE_URL,
    provider: WEKNORA_CLOUD_PROVIDER,
  }

  if (kind === 'embedding' && dimension) {
    parameters.embedding_parameters = {
      dimension,
      truncate_prompt_tokens: 0,
    }
  }
  if (kind === 'vllm') {
    parameters.supports_vision = true
  }

  return {
    name,
    display_name: displayName,
    type,
    source: 'remote',
    description: '',
    parameters,
  }
}
