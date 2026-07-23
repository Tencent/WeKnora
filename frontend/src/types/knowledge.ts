export interface Knowledge {
  id: string
  knowledge_base_id: string
  folder_id: string | null
  title?: string
  file_name?: string
  file_type?: string
  type?: string
  parse_status?: string
  updated_at?: string
}
