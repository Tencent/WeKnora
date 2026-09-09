const { getSettings, saveSettings } = require("../../utils/config");
const { applyNavTitle, applyTabBar, getLocale, getMessages, setLocale, t } = require("../../utils/i18n");

const LANGUAGE_VALUES = ["zh", "en"];

function buildViewModel(locale, languageIndex) {
  const messages = getMessages(locale);
  const languageLabels = [messages.languageZh, messages.languageEn];
  return {
    settingsTitle: messages.settingsTitle,
    settingsSubtitle: messages.settingsSubtitle,
    apiBaseUrlLabel: messages.apiBaseUrl,
    apiBaseUrlPlaceholder: messages.apiBaseUrlPlaceholder,
    apiKeyLabel: messages.apiKey,
    apiKeyPlaceholder: messages.apiKeyPlaceholder,
    languageLabel: messages.language,
    saveSettingsText: messages.saveSettings,
    languageLabels,
    displayLanguageLabel: languageLabels[languageIndex] || languageLabels[0]
  };
}

Page({
  data: {
    settingsTitle: "连接 WeKnora",
    settingsSubtitle: "填写 WeKnora API 地址与租户 API Key。",
    apiBaseUrlLabel: "API 地址",
    apiBaseUrlPlaceholder: "https://your-weknora.example.com",
    apiKeyLabel: "API Key",
    apiKeyPlaceholder: "sk-...",
    languageLabel: "界面语言",
    saveSettingsText: "保存设置",
    displayLanguageLabel: "中文",
    baseUrl: "",
    apiKey: "",
    languageIndex: 0,
    languageLabels: ["中文", "English"]
  },

  refreshI18n() {
    const locale = getLocale();
    let languageIndex = LANGUAGE_VALUES.indexOf(locale);
    if (languageIndex < 0) {
      languageIndex = 0;
    }
    const vm = buildViewModel(locale, languageIndex);
    this.setData({
      settingsTitle: vm.settingsTitle,
      settingsSubtitle: vm.settingsSubtitle,
      apiBaseUrlLabel: vm.apiBaseUrlLabel,
      apiBaseUrlPlaceholder: vm.apiBaseUrlPlaceholder,
      apiKeyLabel: vm.apiKeyLabel,
      apiKeyPlaceholder: vm.apiKeyPlaceholder,
      languageLabel: vm.languageLabel,
      saveSettingsText: vm.saveSettingsText,
      languageLabels: vm.languageLabels,
      displayLanguageLabel: vm.displayLanguageLabel,
      languageIndex: languageIndex
    });
    applyTabBar(locale);
    applyNavTitle("navSettings", locale);
  },

  onLoad() {
    this.refreshI18n();
  },

  onShow() {
    try {
      const settings = getSettings();
      this.setData({
        baseUrl: settings.baseUrl,
        apiKey: settings.apiKey
      });
      this.refreshI18n();
    } catch (error) {
      // keep fallback Chinese copy
    }
  },

  onBaseUrlInput(event) {
    this.setData({ baseUrl: event.detail.value });
  },

  onApiKeyInput(event) {
    this.setData({ apiKey: event.detail.value });
  },

  onLanguageChange(event) {
    const languageIndex = Number(event.detail.value);
    const locale = LANGUAGE_VALUES[languageIndex] || "zh";
    setLocale(locale);
    this.setData({ languageIndex });
    this.refreshI18n();
  },

  save() {
    saveSettings({
      baseUrl: this.data.baseUrl,
      apiKey: this.data.apiKey,
      locale: LANGUAGE_VALUES[this.data.languageIndex] || getLocale()
    });
    this.refreshI18n();
    wx.showToast({ title: t("saved"), icon: "success" });
  }
});
