export type DocumentFolderMentionBlockReason =
  | 'capability-unavailable'
  | 'smart-reasoning'
  | null;

export const resolveDocumentFolderListFilter = (
  capabilityEnabled: boolean,
  documentKnowledgeBaseReady: boolean,
  selectedFolderID: string | undefined,
): string | undefined => (
  capabilityEnabled && documentKnowledgeBaseReady ? selectedFolderID : undefined
);

export const shouldFetchStarterSuggestions = (
  selectedFolderCount: number,
): boolean => selectedFolderCount === 0;

export const runDocumentFolderCapabilityCheck = async (
  lock: { value: boolean },
  check: () => Promise<void>,
): Promise<boolean> => {
  if (lock.value) return false;
  lock.value = true;
  try {
    await check();
    return true;
  } finally {
    lock.value = false;
  }
};

export const applyDocumentFolderSelectionChange = (
  restoredOverrideActive: boolean,
  applyTransient: () => void,
  applyPersistent: () => void,
): void => {
  if (restoredOverrideActive) {
    applyTransient();
    return;
  }
  applyPersistent();
};

export const getDocumentFolderMentionBlockReason = (
  hasSelectedFolders: boolean,
  capabilityEnabled: boolean,
  smartReasoning: boolean,
): DocumentFolderMentionBlockReason => {
  if (!hasSelectedFolders) return null;
  if (!capabilityEnabled) return 'capability-unavailable';
  if (smartReasoning) return 'smart-reasoning';
  return null;
};
