export type MentionItemType = 'kb' | 'folder' | 'file' | 'tag' | 'mcp' | 'skill';

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
  includeDescendants?: boolean;
  orgName?: string;
  serviceId?: string;
  serviceName?: string;
  skillName?: string;
  isAgentConfigured?: boolean;
}

export interface MentionRequestItem {
  id: string;
  name: string;
  type: MentionItemType;
  kb_type?: 'document' | 'faq';
  kb_id?: string;
  include_descendants?: boolean;
  kb_name?: string;
  service_id?: string;
  skill_name?: string;
}
