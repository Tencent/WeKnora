import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

export interface UploadKnowledgeFileData {
  file: File
  tag_ids?: string[]
  fileName?: string
  process_config?: KnowledgeProcessOverrides | string
  folder_id?: string | null
  [key: string]: any
}

export function buildKnowledgeUploadFormData(data: UploadKnowledgeFileData): FormData {
  const formData = new FormData()
  Object.keys(data).forEach((key) => {
    const value = data[key]
    if (value === undefined || value === null) return
    if (key === 'tag_ids' && Array.isArray(value)) {
      formData.append(key, value.join(','))
    } else if (key === 'process_config' && value && typeof value !== 'string') {
      formData.append(key, JSON.stringify(value))
    } else {
      formData.append(key, value)
    }
  })
  return formData
}
