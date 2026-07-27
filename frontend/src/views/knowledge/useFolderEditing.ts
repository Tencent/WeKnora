import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { knowledgeFolderApi } from "@/api/knowledge-base/folders";

// useFolderEditing owns the page-level inline folder editing state machine.
// The shape:
//   { mode: 'create' | 'rename'; folderId: string; value: string; error: string } | null
//
// - `mode === 'rename'`: `folderId` is the folder being renamed. The inline
//   input lives inside FolderGridItems / FolderListRows (driven by the
//   `renamingFolderId` / `renameError` props they already accept).
// - `mode === 'create'`: `folderId` is the PARENT folder id under which the
//   new folder will be created ("new folder parent = current folder").
//
// The page owns the entry-point wiring (create/rename/delete emits from the
// folder components call into this composable's start/commit helpers). This
// composable owns: the state machine, the UX pre-validation (normalize rules),
// the backend error -> inline-message mapping, and the API calls. The service
// remains the authority - frontend normalize is UX pre-validation only; the
// backend re-validates and returns conflict / depth / invalid errors that we
// map back to the inline `error` field, keeping the editing state active so
// the user can fix the name.
//
// Backend name validation (internal/application/service/knowledge_folder.go
// normalizeFolderName): rejects empty, `.`, `..`, or names containing `/` or
// `\` or control characters. The frontend normalize mirrors the visible part
// (non-empty, not `.`/`..`, no slash/backslash); control-char rejection is
// left to the backend and surfaces as a generic 400 invalid error.

export interface FolderEditState {
  mode: "create" | "rename";
  // For 'rename': the folder being renamed. For 'create': the parent folder
  // id under which the new folder will be created.
  folderId: string;
  value: string;
  error: string;
}

// Reduce the varied request-util reject shape ({ status, message, ...data })
// to an inline error message keyed off the HTTP status. The folder handler
// (internal/handler/knowledge_folder.go writeKnowledgeFolderError) maps:
//   - ErrFolderAlreadyExists -> 409 Conflict
//   - ErrInvalidArgument (bad name OR depth > MaxFolderDepth) -> 400 BadRequest
// Since both invalid-name and depth-exceeded share HTTP 400, and the frontend
// pre-validates the name before calling the API, a 400 reaching us after
// pre-validation is most likely depth exceeded. Conflict is cleanly
// distinguishable via 409.
function mapFolderApiError(
  err: unknown,
  t: (key: string) => string,
): string {
  if (err && typeof err === "object") {
    const e = err as { status?: unknown; message?: unknown };
    const status = typeof e.status === "number" ? e.status : 0;
    if (status === 409) {
      return t("knowledgeBase.folderNameConflict");
    }
    if (status === 400) {
      // Name already passed frontend normalize, so a 400 here is most likely
      // depth exceeded (MaxFolderDepth = 10). Control-char names (which the
      // frontend doesn't pre-check) also land here, but depth is the common
      // case after pre-validation, so use the depth message.
      return t("knowledgeBase.folderDepthExceeded");
    }
    if (typeof e.message === "string" && e.message) {
      return e.message;
    }
  }
  if (typeof err === "string" && err) return err;
  return t("knowledgeBase.folderActionFailed");
}

export default function useFolderEditing() {
  const { t } = useI18n();
  const editState = ref<FolderEditState | null>(null);

  // Derived views for binding to folder components' props.
  const renamingFolderId = computed<string | null>(() =>
    editState.value && editState.value.mode === "rename"
      ? editState.value.folderId
      : null,
  );
  const renameError = computed<string>(() =>
    editState.value && editState.value.mode === "rename"
      ? editState.value.error
      : "",
  );
  const creatingParentId = computed<string | null>(() =>
    editState.value && editState.value.mode === "create"
      ? editState.value.folderId
      : null,
  );
  const isEditing = computed(() => editState.value !== null);

  function startCreate(parentId: string): void {
    editState.value = {
      mode: "create",
      folderId: parentId,
      value: "",
      error: "",
    };
  }

  function startRename(folderId: string): void {
    editState.value = {
      mode: "rename",
      folderId,
      value: "",
      error: "",
    };
  }

  function cancelEdit(): void {
    editState.value = null;
  }

  function setError(message: string): void {
    if (editState.value) {
      editState.value = { ...editState.value, error: message };
    }
  }

  function clearError(): void {
    if (editState.value && editState.value.error) {
      editState.value = { ...editState.value, error: "" };
    }
  }

  // Frontend UX pre-validation (service remains authority). Mirrors the
  // visible part of the backend normalizeFolderName rules: non-empty, not
  // `.`/`..`, no slash/backslash. Returns the trimmed name on success, or a
  // localized error string on failure.
  function normalizeFolderName(name: string):
    | { name: string }
    | { error: string } {
    const trimmed = name.trim();
    if (!trimmed) {
      return { error: t("knowledgeBase.folderNameRequired") };
    }
    if (trimmed === "." || trimmed === "..") {
      return { error: t("knowledgeBase.folderNameInvalid") };
    }
    if (trimmed.includes("/") || trimmed.includes("\\")) {
      return { error: t("knowledgeBase.folderNameInvalid") };
    }
    return { name: trimmed };
  }

  // Commit a create. On success: clear edit state and call refresh so the
  // new folder appears in direct folders / tree / breadcrumb WITHOUT changing
  // the URL (the URL stays on the current folder). On error: map the backend
  // error to an inline message and KEEP the editing state so the user can fix
  // the name.
  async function commitCreate(
    kbId: string,
    name: string,
    refresh: () => Promise<void>,
  ): Promise<void> {
    if (!editState.value || editState.value.mode !== "create") return;
    const normalized = normalizeFolderName(name);
    if ("error" in normalized) {
      setError(normalized.error);
      return;
    }
    try {
      await knowledgeFolderApi.create(kbId, {
        parent_id: editState.value.folderId,
        name: normalized.name,
      });
      cancelEdit();
      await refresh();
    } catch (err: unknown) {
      setError(mapFolderApiError(err, t));
    }
  }

  // Commit a rename. Same success/error contract as commitCreate: on success
  // refresh without changing URL; on error keep editing state with inline msg.
  async function commitRename(
    kbId: string,
    folderId: string,
    name: string,
    refresh: () => Promise<void>,
  ): Promise<void> {
    if (
      !editState.value ||
      editState.value.mode !== "rename" ||
      editState.value.folderId !== folderId
    ) {
      return;
    }
    const normalized = normalizeFolderName(name);
    if ("error" in normalized) {
      setError(normalized.error);
      return;
    }
    try {
      await knowledgeFolderApi.rename(kbId, folderId, normalized.name);
      cancelEdit();
      await refresh();
    } catch (err: unknown) {
      setError(mapFolderApiError(err, t));
    }
  }

  return {
    editState,
    renamingFolderId,
    renameError,
    creatingParentId,
    isEditing,
    startCreate,
    startRename,
    cancelEdit,
    setError,
    clearError,
    normalizeFolderName,
    commitCreate,
    commitRename,
  };
}
