export type MentionItemType = 'kb' | 'file' | 'tag' | 'folder' | 'mcp' | 'skill';

export interface MentionItem {
  id: string;
  name: string;
  type: MentionItemType;
  group?: string;
  description?: string;
  kbType?: 'document' | 'faq';
  count?: number;
  kbName?: string;
  kbId?: string;
  orgName?: string;
  serviceId?: string;
  serviceName?: string;
  skillName?: string;
  isAgentConfigured?: boolean;
  // folder-specific: the materialized path for display (e.g. "Root/Sub/Folder")
  folderPath?: string;
  // file-specific: direct folder membership, used to resolve the visible path
  folderId?: string;
  parentId?: string;
  documentCount?: number;
  hasChildren?: boolean;
  supportsDocumentFolders?: boolean;
}

export interface MentionRequestItem {
  id: string;
  name: string;
  type: MentionItemType;
  kb_type?: 'document' | 'faq';
  kb_id?: string;
  kb_name?: string;
  service_id?: string;
  skill_name?: string;
}
