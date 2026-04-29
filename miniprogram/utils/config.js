const STORAGE_KEY = "weknora_settings";

function normalizeBaseUrl(baseUrl) {
  if (!baseUrl || typeof baseUrl !== "string") {
    return "";
  }

  return baseUrl.trim().replace(/\/+$/, "");
}

function getSettings() {
  const stored = wx.getStorageSync(STORAGE_KEY) || {};
  return {
    baseUrl: normalizeBaseUrl(stored.baseUrl || ""),
    apiKey: stored.apiKey || "",
    selectedKnowledgeBaseId: stored.selectedKnowledgeBaseId || ""
  };
}

function saveSettings(settings) {
  const current = getSettings();
  const next = {
    ...current,
    ...settings,
    baseUrl: normalizeBaseUrl(settings.baseUrl ?? current.baseUrl)
  };
  wx.setStorageSync(STORAGE_KEY, next);
  return next;
}

module.exports = {
  STORAGE_KEY,
  getSettings,
  normalizeBaseUrl,
  saveSettings
};
