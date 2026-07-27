import { ref } from "vue";
import { knowledgeFolderApi } from "@/api/knowledge-base/folders";
import { batchReparseKnowledge } from "@/api/knowledge-base/index";
import type {
  BatchReparseFolderRequest,
  FileSystemSelection,
} from "@/types/knowledgeFolder";
import type { KnowledgeProcessOverrides } from "@/types/knowledgeProcess";

// useFolderOperations owns the same-KB batch operations on the folder-aware
// knowledge page: move-to-folder, recursive delete, and reparse. It is the
// page-level counterpart to useFolderEditing (create/rename) and is kept
// separate from the cross-KB transfer flow (which lives in KnowledgeBase.vue
// and is document-only). The operations are distinct:
//   - 移动到文件夹 (same KB, files + folders)  -> batchMove
//   - 递归删除文件夹/文档 (same KB)            -> batchDelete
//   - 重新解析 (same KB, files only)           -> batchReparse
//
// CRITICAL: recursive delete ALWAYS uses
// `POST /api/v1/knowledge/batch-delete`. The backend expands the selected
// folder_ids to their full descendant subtrees async.
//
// Reparse uses `POST /api/v1/knowledge/batch-reparse` with knowledge_ids
// only - folder scope was removed. The backend enforces a per-request
// document cap (REPARSE_LIMIT); when the client-side count is known we
// pre-disable the action to avoid a round-trip.
//
// All three methods:
//   1. Split the FileSystemSelection into stable-sorted knowledge_ids /
//      folder_ids arrays (deterministic payload for debugging/snapshots).
//   2. Call the batch API.
//   3. On success, call the page-supplied refresh callback (onDone) so the
//      page can invalidate folder tree / direct docs / breadcrumb / URL.
//   4. On error, rethrow so the page can surface a MessagePlugin error or an
//      inline dialog error. The composable does NOT call MessagePlugin itself
//      - presentation stays with the page, matching useFolderEditing.
//
// "No task-completion guarantee": batch-delete and batch-reparse return a
// task_id and run async on the backend. onDone invalidates the local cache so
// the UI eventually converges, but the composable never claims the items are
// already deleted/reprocessed - that wording lives in the page's submitted
// toast (folderDeleteSubmitted / batchReparseSuccess), not here.

// Backend per-request cap on batch-reparse document count. Used to pre-disable
// the reparse action when the client-side count is known, avoiding a rejected
// round-trip. Exported so the page can bind the same limit to its computed
// pre-disable flag and batch-bar tooltip.
export const REPARSE_LIMIT = 200;

// Reduce the varied request-util reject shape ({ status, message, ...data })
// to a non-empty string for display. Mirrors the helper in useKnowledgeFolders
// so error messages are consistent across folder operations.
function extractErrorMessage(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { message?: unknown; status?: unknown };
    if (typeof e.message === "string" && e.message) return e.message;
    if (typeof e.status === "number") return `HTTP ${e.status}`;
  }
  if (typeof err === "string" && err) return err;
  return "error";
}

// Split a FileSystemSelection into the batch payload arrays, each
// stable-sorted for deterministic output. Shared by move / delete
// since both batch endpoints accept the same { knowledge_ids, folder_ids }
// scope shape. (Reparse no longer accepts folder scope and builds its
// knowledge_ids-only payload directly in reparseSelection.)
//
//   file-only   -> knowledge_ids: [id,...], folder_ids: []
//   folder-only -> knowledge_ids: [],        folder_ids: [id,...]
//   mixed       -> knowledge_ids: [ids],     folder_ids: [ids]
//
// Both keys are always present (possibly empty) so the backend receives a
// stable shape regardless of case.
function buildScopePayload(selection: FileSystemSelection): {
  knowledge_ids: string[];
  folder_ids: string[];
} {
  return {
    knowledge_ids: [...selection.knowledgeIds].sort(),
    folder_ids: [...selection.folderIds].sort(),
  };
}

/**
 * Evaluate the known reparse document count for a selection and whether it
 * exceeds the backend per-request limit. Pure so the page can call it from a
 * computed to pre-disable the reparse button.
 *
 * Only knowledge_ids are counted - batch-reparse no longer accepts folder
 * scope. The page is expected to have already excluded in-flight docs before
 * calling, but in-flight filtering happens in reparseSelection too; this
 * helper counts the selection as given.
 */
export function evaluateReparseLimit(
  selection: FileSystemSelection,
): { count: number; overLimit: boolean } {
  const count = selection.knowledgeIds.size;
  return { count, overLimit: count > REPARSE_LIMIT };
}

export default function useFolderOperations() {
  // moving / deleting / reparsing gate their respective dialog confirm
  // buttons + the page's action state. *Error holds the last error message
  // (cleared on the next attempt) so a dialog can show an inline error
  // alongside its content if desired.
  const moving = ref(false);
  const moveError = ref<string | null>(null);
  const deleting = ref(false);
  const deleteError = ref<string | null>(null);
  const reparsing = ref(false);
  const reparseError = ref<string | null>(null);

  async function moveWithinKnowledgeBase(
    kbId: string,
    selection: FileSystemSelection,
    targetFolderId: string,
    onDone: () => Promise<void> | void,
  ): Promise<void> {
    moving.value = true;
    moveError.value = null;
    try {
      const { knowledge_ids, folder_ids } = buildScopePayload(selection);
      await knowledgeFolderApi.batchMove({
        kb_id: kbId,
        knowledge_ids,
        folder_ids,
        target_folder_id: targetFolderId,
      });
      await onDone();
    } catch (err: unknown) {
      moveError.value = extractErrorMessage(err);
      // Rethrow so the page can surface a MessagePlugin / inline error.
      throw err;
    } finally {
      moving.value = false;
    }
  }

  // Recursive delete via `POST /api/v1/knowledge/batch-delete`. The backend
  // expands folder_ids to their full descendant subtrees and returns a task_id
  // (the actual deletion is async). Returns the task_id so the page can
  // (optionally) track it; the page must NOT treat the returned task_id as
  // "deletion complete" - it only means "submitted". onDone is called after
  // the submit resolves so the page can invalidate + navigate before refresh.
  async function deleteFolders(
    kbId: string,
    selection: FileSystemSelection,
    onDone: () => Promise<void> | void,
  ): Promise<string> {
    deleting.value = true;
    deleteError.value = null;
    try {
      const { knowledge_ids, folder_ids } = buildScopePayload(selection);
      const res: {
        success?: boolean;
        message?: string;
        data?: { task_id?: string; deleted_count?: number };
      } = await knowledgeFolderApi.batchDelete({
        kb_id: kbId,
        knowledge_ids,
        folder_ids,
      });
      if (!res?.success) {
        // Surface the backend message and throw so the page shows a
        // consistent error toast.
        const msg = res?.message || "delete failed";
        throw new Error(msg);
      }
      const taskId = res?.data?.task_id ?? "";
      await onDone();
      return taskId;
    } catch (err: unknown) {
      deleteError.value = extractErrorMessage(err);
      throw err;
    } finally {
      deleting.value = false;
    }
  }

  // Reparse submit via `POST /api/v1/knowledge/batch-reparse` with
  // knowledge_ids only (folder scope was removed). Filters in-flight knowledge
  // docs using the page-supplied predicate (the page owns cardList /
  // parse_status; the composable does not).
  //
  // Returns the submitted (filtered) knowledge_ids so the page can run its
  // optimistic pending-ack UI. The backend 200-limit (REPARSE_LIMIT) is
  // enforced server-side; the error is rethrown for the page to display.
  // onDone is called after the submit resolves so the page can invalidate
  // caches.
  async function reparseSelection(
    kbId: string,
    selection: FileSystemSelection,
    onDone: () => Promise<void> | void,
    options?: {
      isInFlight?: (knowledgeId: string) => boolean;
      processConfig?: KnowledgeProcessOverrides;
    },
  ): Promise<string[]> {
    reparsing.value = true;
    reparseError.value = null;
    try {
      // Filter in-flight knowledge docs: the page's predicate knows the parse
      // status; an id with no card row is treated as reparse-able (the backend
      // will reject unknown ids).
      const isInFlight = options?.isInFlight;
      const knowledge_ids = isInFlight
        ? [...selection.knowledgeIds].sort().filter((id) => !isInFlight(id))
        : [...selection.knowledgeIds].sort();
      const payload: BatchReparseFolderRequest = {
        kb_id: kbId,
        knowledge_ids,
      };
      if (options?.processConfig) {
        payload.process_config = options.processConfig;
      }
      const res: { success?: boolean; message?: string } = await batchReparseKnowledge(
        payload,
      );
      if (!res?.success) {
        // Surface the backend message (e.g. 200-limit rejection) and throw so
        // the page shows a consistent error toast.
        const msg = res?.message || "reparse failed";
        throw new Error(msg);
      }
      await onDone();
      return knowledge_ids;
    } catch (err: unknown) {
      reparseError.value = extractErrorMessage(err);
      throw err;
    } finally {
      reparsing.value = false;
    }
  }

  return {
    moving,
    moveError,
    deleting,
    deleteError,
    reparsing,
    reparseError,
    buildScopePayload,
    moveWithinKnowledgeBase,
    deleteFolders,
    reparseSelection,
  };
}
