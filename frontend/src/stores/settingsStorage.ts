import { safeRemoveItem, safeSetItem } from "@/composables/preferenceStorage";
import { reconcileBuiltinAgentMode } from "@/utils/agent-mode";

export const SETTINGS_STORAGE_KEY = "WeKnora_settings";

/** Deep-clone settings so nested arrays/objects are not shared with defaults. */
export function cloneSettings<T>(settings: T): T {
  return JSON.parse(JSON.stringify(settings));
}

export function isStoredSettingsRecord(
  value: unknown,
): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

type ReconcilableSettings = {
  selectedTags?: unknown;
  selectedFolderScopes?: unknown;
  selectedMCPServices?: unknown;
  selectedSkills?: unknown;
  selectedTools?: unknown;
  selectedFileKbMap?: unknown;
  enableMemory?: unknown;
  isAgentEnabled: boolean;
  selectedAgentId?: string;
};

function reconcileLoadedSettings<T extends ReconcilableSettings>(loaded: T): T {
  loaded.selectedTags ||= [];
  if (!isStoredSettingsRecord(loaded.selectedFolderScopes)) {
    loaded.selectedFolderScopes = {};
  }
  loaded.selectedMCPServices ||= [];
  loaded.selectedSkills ||= (loaded.selectedTools as string[] | undefined) || [];
  loaded.selectedFileKbMap ||= {};
  const removedLegacyMemorySetting = Object.prototype.hasOwnProperty.call(loaded, "enableMemory");
  if (removedLegacyMemorySetting) {
    delete loaded.enableMemory;
  }
  const reconciledAgentMode = reconcileBuiltinAgentMode(loaded);
  if (removedLegacyMemorySetting || reconciledAgentMode) {
    safeSetItem(SETTINGS_STORAGE_KEY, JSON.stringify(loaded));
  }
  return loaded;
}

function resetStoredSettings<T extends ReconcilableSettings>(
  defaultSettings: T,
  reason: unknown,
): T {
  console.error(
    "[settings] Failed to parse WeKnora_settings from localStorage, resetting to defaults:",
    reason,
  );
  safeRemoveItem(SETTINGS_STORAGE_KEY);
  return reconcileLoadedSettings(cloneSettings(defaultSettings));
}

/** Load settings from localStorage, reconcile builtin agent mode, fall back on corruption. */
export function loadAndReconcileSettings<T extends ReconcilableSettings>(
  defaultSettings: T,
): T {
  try {
    const raw = localStorage.getItem(SETTINGS_STORAGE_KEY);
    if (!raw) {
      return reconcileLoadedSettings(cloneSettings(defaultSettings));
    }
    const parsed: unknown = JSON.parse(raw);
    if (!isStoredSettingsRecord(parsed)) {
      return resetStoredSettings(
        defaultSettings,
        new Error("stored value is not a settings object"),
      );
    }
    return reconcileLoadedSettings(parsed as T);
  } catch (e) {
    return resetStoredSettings(defaultSettings, e);
  }
}
