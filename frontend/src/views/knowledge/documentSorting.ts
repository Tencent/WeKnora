import type {
  KnowledgeListSortField,
  KnowledgeListSortOrder,
} from '@/api/knowledge-base';

export type DocumentSortValue =
  | 'updated_desc'
  | 'updated_asc'
  | 'created_desc'
  | 'created_asc'
  | 'name_asc'
  | 'name_desc';

export interface DocumentSortOption {
  value: DocumentSortValue;
  sortBy: KnowledgeListSortField;
  sortOrder: KnowledgeListSortOrder;
  groupKey: 'updatedTime' | 'createdTime' | 'fileName';
  labelKey:
    | 'recentlyUpdated'
    | 'earliestUpdated'
    | 'newestCreated'
    | 'earliestCreated'
    | 'nameAscending'
    | 'nameDescending';
}

export const DEFAULT_DOCUMENT_SORT: DocumentSortValue = 'updated_desc';

export const DOCUMENT_SORT_OPTIONS: readonly DocumentSortOption[] = [
  {
    value: 'updated_desc',
    sortBy: 'updated_at',
    sortOrder: 'desc',
    groupKey: 'updatedTime',
    labelKey: 'recentlyUpdated',
  },
  {
    value: 'updated_asc',
    sortBy: 'updated_at',
    sortOrder: 'asc',
    groupKey: 'updatedTime',
    labelKey: 'earliestUpdated',
  },
  {
    value: 'created_desc',
    sortBy: 'created_at',
    sortOrder: 'desc',
    groupKey: 'createdTime',
    labelKey: 'newestCreated',
  },
  {
    value: 'created_asc',
    sortBy: 'created_at',
    sortOrder: 'asc',
    groupKey: 'createdTime',
    labelKey: 'earliestCreated',
  },
  {
    value: 'name_asc',
    sortBy: 'file_name',
    sortOrder: 'asc',
    groupKey: 'fileName',
    labelKey: 'nameAscending',
  },
  {
    value: 'name_desc',
    sortBy: 'file_name',
    sortOrder: 'desc',
    groupKey: 'fileName',
    labelKey: 'nameDescending',
  },
];

export function getDocumentSortOption(value: DocumentSortValue): DocumentSortOption {
  return DOCUMENT_SORT_OPTIONS.find((option) => option.value === value)
    ?? DOCUMENT_SORT_OPTIONS[0];
}

export function getDocumentSortParams(value: DocumentSortValue): {
  sort_by: KnowledgeListSortField;
  sort_order: KnowledgeListSortOrder;
} {
  const option = getDocumentSortOption(value);
  return {
    sort_by: option.sortBy,
    sort_order: option.sortOrder,
  };
}
