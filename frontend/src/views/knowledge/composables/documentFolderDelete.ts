import type {
  DocumentFolderDeleteImpact,
  DocumentFolderDeleteMode,
} from '@/api/knowledge-base';

export interface DocumentFolderDeletePlan {
  kind: 'empty-folder' | 'empty-tree' | 'documents';
  defaultMode: DocumentFolderDeleteMode | '' | undefined;
  keepDocumentsDisabled: boolean;
}

export function buildDocumentFolderDeletePlan(
  impact: DocumentFolderDeleteImpact,
): DocumentFolderDeletePlan {
  if (impact.document_count === 0) {
    return {
      kind: impact.folder_count > 1 ? 'empty-tree' : 'empty-folder',
      defaultMode: impact.folder_count > 1 ? 'keep_documents' : undefined,
      keepDocumentsDisabled: false,
    };
  }

  const keepDocumentsDisabled = impact.active_document_count > 0;
  return {
    kind: 'documents',
    defaultMode: keepDocumentsDisabled ? '' : 'keep_documents',
    keepDocumentsDisabled,
  };
}
