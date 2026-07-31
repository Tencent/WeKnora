import type { DocumentFolderSelection } from '../components/documentFolderViewTypes';

type RouteQueryValue = string | null | Array<string | null> | undefined;
export type DocumentFolderRouteQuery = Record<string, RouteQueryValue>;

export const DOCUMENT_FOLDER_ROUTE_QUERY_KEYS = [
  'folder_scope',
  'folder_id',
  'folder_path',
  'folder_trail',
] as const;

interface DocumentFolderRouteLabels {
  root: string;
  all: string;
}

function queryString(value: RouteQueryValue): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function rootSelection(name: string): DocumentFolderSelection {
  return {
    id: '',
    kind: 'root',
    name,
    path: [],
    trail: [],
  };
}

export function documentFolderSelectionFromRouteQuery(
  query: DocumentFolderRouteQuery,
  labels: DocumentFolderRouteLabels,
): DocumentFolderSelection {
  if (queryString(query.folder_scope) === 'all') {
    return {
      id: undefined,
      kind: 'all',
      name: labels.all,
      path: [],
      trail: [],
    };
  }

  const id = queryString(query.folder_id)?.trim();
  const rawPath = queryString(query.folder_path);
  if (!id || !rawPath) return rootSelection(labels.root);

  const path = rawPath.split('/');
  if (path.some(segment => segment.length === 0)) return rootSelection(labels.root);

  const rawTrail = queryString(query.folder_trail);
  const trailIDs = rawTrail ? rawTrail.split('/') : [];
  const hasCompleteTrail = trailIDs.length === path.length
    && trailIDs.every(Boolean)
    && trailIDs[trailIDs.length - 1] === id;

  return {
    id,
    kind: 'folder',
    name: path[path.length - 1],
    path,
    trail: hasCompleteTrail
      ? path.map((name, index) => ({ id: trailIDs[index], name }))
      : [],
  };
}

export function withDocumentFolderSelectionRouteQuery(
  query: DocumentFolderRouteQuery,
  selection: DocumentFolderSelection,
): DocumentFolderRouteQuery {
  const nextQuery: DocumentFolderRouteQuery = { ...query };
  DOCUMENT_FOLDER_ROUTE_QUERY_KEYS.forEach(key => delete nextQuery[key]);

  if (selection.kind === 'all') {
    nextQuery.folder_scope = 'all';
    return nextQuery;
  }

  if (selection.kind !== 'folder' || !selection.id || selection.path.length === 0) {
    return nextQuery;
  }

  nextQuery.folder_id = selection.id;
  nextQuery.folder_path = selection.path.join('/');

  const trailIDs = (selection.trail || []).map(crumb => crumb.id);
  if (
    trailIDs.length === selection.path.length
    && trailIDs[trailIDs.length - 1] === selection.id
  ) {
    nextQuery.folder_trail = trailIDs.join('/');
  }

  return nextQuery;
}

export function isSameDocumentFolderLocation(
  left: DocumentFolderSelection,
  right: DocumentFolderSelection,
): boolean {
  if (left.kind !== right.kind) return false;
  return left.kind !== 'folder' || left.id === right.id;
}

function comparableQueryValue(value: RouteQueryValue): string {
  if (Array.isArray(value)) return `array:${value.map(item => item ?? '').join('\u0000')}`;
  if (value === null) return 'null';
  return value === undefined ? 'undefined' : `string:${value}`;
}

export function hasSameDocumentFolderRouteQuery(
  left: DocumentFolderRouteQuery,
  right: DocumentFolderRouteQuery,
): boolean {
  return DOCUMENT_FOLDER_ROUTE_QUERY_KEYS.every(key => (
    comparableQueryValue(left[key]) === comparableQueryValue(right[key])
  ));
}
