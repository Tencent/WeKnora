const STORAGE_KEY = "weknora_settings";

function normalizeBaseUrl(baseUrl) {
  if (!baseUrl || typeof baseUrl !== "string") {
    return "";
  }

  return baseUrl.trim().replace(/\/+$/, "");
}

function normalizeLocale(locale) {
  return locale === "en" ? "en" : "zh";
}

function getSettings() {
  const stored = wx.getStorageSync(STORAGE_KEY) || {};
  return {
    baseUrl: normalizeBaseUrl(stored.baseUrl || ""),
    apiKey: stored.apiKey || "",
    selectedKnowledgeBaseId: stored.selectedKnowledgeBaseId || "",
    locale: normalizeLocale(stored.locale || "zh")
  };
}

function saveSettings(settings) {
  const current = getSettings();
  const nextBaseUrl =
    settings.baseUrl !== undefined && settings.baseUrl !== null
      ? settings.baseUrl
      : current.baseUrl;
  const nextLocale =
    settings.locale !== undefined && settings.locale !== null
      ? settings.locale
      : current.locale;
  const next = {
    baseUrl: normalizeBaseUrl(nextBaseUrl),
    apiKey: settings.apiKey !== undefined ? settings.apiKey : current.apiKey,
    selectedKnowledgeBaseId:
      settings.selectedKnowledgeBaseId !== undefined
        ? settings.selectedKnowledgeBaseId
        : current.selectedKnowledgeBaseId,
    locale: normalizeLocale(nextLocale)
  };
  wx.setStorageSync(STORAGE_KEY, next);
  return next;
}

module.exports = {
  STORAGE_KEY,
  getSettings,
  normalizeBaseUrl,
  normalizeLocale,
  saveSettings
};
