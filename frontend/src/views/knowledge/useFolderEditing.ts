import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { knowledgeFolderApi } from "@/api/knowledge-base/folders";

export interface FolderEditState {
  mode: "create" | "rename";
  folderId: string;
  surface: "tree" | "content";
  value: string;
  error: string;
}

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
    editState.value && editState.value.mode === "create" && editState.value.surface === "tree"
      ? editState.value.folderId
      : null,
  );
  const isEditing = computed(() => editState.value !== null);

  // Which surface is currently in create mode (null when not creating or when
  // renaming). The page uses this to gate the content-area input
  // (surface === 'content') vs passing creatingParentId to the tree
  // (surface === 'tree').
  const creatingSurface = computed<"tree" | "content" | null>(() =>
    editState.value && editState.value.mode === "create" ? editState.value.surface : null,
  );

  // Inline error message for the active create (empty when no create or no
  // error). The tree surfaces this as createError; the content area does NOT
  // display it (out of scope).
  const createError = computed<string>(() =>
    editState.value && editState.value.mode === "create" ? editState.value.error : "",
  );

  function startCreate(parentId: string, surface: "tree" | "content" = "content"): void {
    editState.value = {
      mode: "create",
      folderId: parentId,
      surface,
      value: "",
      error: "",
    };
  }

  function startRename(folderId: string): void {
    editState.value = {
      mode: "rename",
      folderId,
      // Rename is always content-surface (card/row inline). Unused in this mode.
      surface: "content",
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
    creatingSurface,
    createError,
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
