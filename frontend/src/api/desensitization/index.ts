import { post } from '@/utils/request'

export function previewDesensitization(body: {
  text: string
  desensitization_config: {
    enabled?: boolean
    engine?: string
    mask_style?: string
    entity_types?: string[]
    rules?: { name: string; pattern: string; replacement?: string }[]
  }
}) {
  return post('/api/v1/desensitization/preview', body)
}
