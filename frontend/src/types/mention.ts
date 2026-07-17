export type MentionItemType = 'kb' | 'file' | 'tag' | 'mcp' | 'skill' | 'folder' | `folder:${string}`;

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
  // folder tree fields
  parentId?: string;
  depth?: number;
  hasChildren?: boolean;
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
