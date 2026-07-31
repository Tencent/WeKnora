export type DocumentFolderSelectionKind = 'all' | 'root' | 'folder';

export interface DocumentFolderBreadcrumb {
  id: string;
  name: string;
}

export interface DocumentFolderSelection {
  id: string | undefined;
  kind: DocumentFolderSelectionKind;
  name: string;
  path: string[];
  trail?: DocumentFolderBreadcrumb[];
}

export const createRootDocumentFolderSelection = (
  name: string,
): DocumentFolderSelection => ({
  id: '',
  kind: 'root',
  name,
  path: [],
  trail: [],
});
