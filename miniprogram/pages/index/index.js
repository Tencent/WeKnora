const { getSettings, saveSettings } = require("../../utils/config");
const { createKnowledgeFromURL, listKnowledgeBases } = require("../../utils/request");
const { applyNavTitle, applyTabBar, getMessages, t } = require("../../utils/i18n");

function normalizeKnowledgeBases(response) {
  if (!response) {
    return [];
  }
  if (Array.isArray(response.data)) {
    return response.data;
  }
  if (response.data && Array.isArray(response.data.list)) {
    return response.data.list;
  }
  if (Array.isArray(response.knowledge_bases)) {
    return response.knowledge_bases;
  }
  return [];
}

function buildViewModel() {
  const messages = getMessages();
  return {
    texts: messages,
    knowledgeTitle: messages.knowledgeTitle,
    knowledgeSubtitle: messages.knowledgeSubtitle,
    needsSettingsText: messages.needsSettings,
    openSettingsText: messages.openSettings,
    knowledgeBaseLabel: messages.knowledgeBase,
    tapToSelectText: messages.tapToSelect,
    refreshText: messages.refreshKnowledgeBases,
    urlLabel: messages.urlLabel,
    urlPlaceholder: messages.urlPlaceholder,
    importUrlText: messages.importUrl
  };
}

Page({
  data: {
    texts: {},
    knowledgeTitle: "WeKnora 知识库",
    knowledgeSubtitle: "在微信中选择知识库，导入网页或提问。",
    needsSettingsText: "请先配置 WeKnora API 地址和 API Key，再加载知识库。",
    openSettingsText: "打开设置",
    knowledgeBaseLabel: "知识库",
    tapToSelectText: "点击选择",
    refreshText: "刷新知识库列表",
    urlLabel: "网页链接",
    urlPlaceholder: "https://github.com/Tencent/WeKnora",
    importUrlText: "导入链接",
    displayKnowledgeBaseName: "点击选择",
    importing: false,
    knowledgeBases: [],
    knowledgeBaseNames: [],
    loading: false,
    needsSettings: false,
    selectedIndex: 0,
    selectedKnowledgeBaseId: "",
    selectedKnowledgeBaseName: "",
    statusMessage: "",
    url: ""
  },

  applyI18n() {
    const vm = buildViewModel();
    const displayKnowledgeBaseName =
      this.data.selectedKnowledgeBaseName || vm.tapToSelectText;
    this.setData({
      texts: vm.texts,
      knowledgeTitle: vm.knowledgeTitle,
      knowledgeSubtitle: vm.knowledgeSubtitle,
      needsSettingsText: vm.needsSettingsText,
      openSettingsText: vm.openSettingsText,
      knowledgeBaseLabel: vm.knowledgeBaseLabel,
      tapToSelectText: vm.tapToSelectText,
      refreshText: vm.refreshText,
      urlLabel: vm.urlLabel,
      urlPlaceholder: vm.urlPlaceholder,
      importUrlText: vm.importUrlText,
      displayKnowledgeBaseName: displayKnowledgeBaseName
    });
  },

  onLoad() {
    this.applyI18n();
  },

  onShow() {
    try {
      applyTabBar();
      applyNavTitle("navKnowledge");
      this.applyI18n();

      const settings = getSettings();
      const needsSettings = !settings.baseUrl || !settings.apiKey;
      const patch = { needsSettings };
      if (settings.selectedKnowledgeBaseId) {
        patch.selectedKnowledgeBaseId = settings.selectedKnowledgeBaseId;
      }
      this.setData(patch);

      if (needsSettings) {
        return;
      }
      this.loadKnowledgeBases();
    } catch (error) {
      this.setData({
        needsSettings: true,
        statusMessage: (error && error.message) || "page init failed"
      });
    }
  },

  onUrlInput(event) {
    this.setData({ url: event.detail.value });
  },

  onKnowledgeBaseChange(event) {
    const selectedIndex = Number(event.detail.value);
    this.selectKnowledgeBase(selectedIndex);
  },

  onKnowledgeBaseTap(event) {
    this.selectKnowledgeBase(Number(event.currentTarget.dataset.index));
  },

  selectKnowledgeBase(selectedIndex) {
    const selected = this.data.knowledgeBases[selectedIndex];
    if (!selected) return;

    saveSettings({ selectedKnowledgeBaseId: selected.id });
    this.setData({
      selectedIndex,
      selectedKnowledgeBaseId: selected.id,
      selectedKnowledgeBaseName: selected.name,
      displayKnowledgeBaseName: selected.name || this.data.tapToSelectText
    });
  },

  openSettings() {
    wx.switchTab({ url: "/pages/settings/settings" });
  },

  async loadKnowledgeBases() {
    const settings = getSettings();
    if (!settings.baseUrl || !settings.apiKey) {
      this.setData({ needsSettings: true });
      return;
    }

    this.setData({ loading: true, statusMessage: "" });
    try {
      const response = await listKnowledgeBases();
      const knowledgeBases = normalizeKnowledgeBases(response);
      const knowledgeBaseNames = knowledgeBases.map((item) => item.name || item.id);
      const current = getSettings();
      let selectedIndex = knowledgeBases.findIndex(
        (item) => item.id === current.selectedKnowledgeBaseId
      );
      if (selectedIndex < 0) {
        selectedIndex = 0;
      }
      const selected = knowledgeBases[selectedIndex];
      const selectedName = (selected && selected.name) || "";
      this.setData({
        knowledgeBases,
        knowledgeBaseNames,
        selectedIndex,
        selectedKnowledgeBaseId: (selected && selected.id) || "",
        selectedKnowledgeBaseName: selectedName,
        displayKnowledgeBaseName: selectedName || this.data.tapToSelectText,
        statusMessage: knowledgeBases.length
          ? t("loadedKnowledgeBases", { count: knowledgeBases.length })
          : t("noKnowledgeBases")
      });
      if (selected && selected.id) {
        saveSettings({ selectedKnowledgeBaseId: selected.id });
      }
    } catch (error) {
      wx.showModal({
        title: t("loadFailed"),
        content: (error && error.message) || "",
        showCancel: false
      });
    } finally {
      this.setData({ loading: false });
    }
  },

  async importURL() {
    this.setData({ importing: true });
    try {
      await createKnowledgeFromURL(this.data.selectedKnowledgeBaseId, this.data.url.trim(), false);
      this.setData({ url: "" });
      wx.showToast({ title: t("imported"), icon: "success" });
    } catch (error) {
      wx.showModal({
        title: t("importFailed"),
        content: (error && error.message) || "",
        showCancel: false
      });
    } finally {
      this.setData({ importing: false });
    }
  }
});
