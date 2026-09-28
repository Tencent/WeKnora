import { get, post, put } from '@/utils/request'

export interface CatalogModel {
  id: string
  type?: string
  match?: string
  name?: string
  context_window?: number
  max_output_tokens?: number
  reasoning?: boolean
  deprecated?: boolean
  [key: string]: unknown
}
export interface CatalogProvider {
  id: string
  name: string
  api: string
  model_types: string[]
  settings?: Record<string, unknown>
  models: CatalogModel[] | null
  // Levels each chat model offers once thinking is on, resolved server-side.
  model_thinking_levels?: Record<string, string[]>
  vendor_thinking_levels?: string[]
}
export interface CatalogOverlay { providers: Record<string, Record<string, any>> }
export interface CatalogState {
  version: number
  baseline: string
  overlay: CatalogOverlay
  history: Array<{ version: number; overlay: CatalogOverlay; updated_by: string; updated_at: string }>
  builtin: CatalogProvider[]
  deployment: CatalogProvider[]
  effective: CatalogProvider[]
  deployment_error?: string
  sync_error?: string
  applied_version: number
}
// A check returns only the candidate; history and the lower layers are omitted.
export type CatalogPreview = Omit<CatalogState, 'history' | 'builtin' | 'deployment'>
export interface CatalogUpdate { version: number; baseline: string; overlay: CatalogOverlay }
const root = '/api/v1/system/admin/model-catalog'
export const getModelCatalog = () => get(root) as unknown as Promise<CatalogState>
export const previewModelCatalog = (request: CatalogUpdate) => post(`${root}/preview`, request) as unknown as Promise<CatalogPreview>
export const publishModelCatalog = (request: CatalogUpdate) => put(root, request) as unknown as Promise<CatalogState>
