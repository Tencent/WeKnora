const { saveSettings, getSettings } = require("../../utils/config");
const { createKnowledgeFromURL, listKnowledgeBases } = require("../../utils/request");

function normalizeKnowledgeBases(response) {
  if (Array.isArray(response?.data)) {
    return response.data;
  }
  if (Array.isArray(response?.data?.list)) {
    return response.data.list;
  }
  if (Array.isArray(response?.knowledge_bases)) {
    return response.knowledge_bases;
  }
  return [];
}

Page({
  data: {
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

  onShow() {
    const settings = getSettings();
    const needsSettings = !settings.baseUrl || !settings.apiKey;
    if (settings.selectedKnowledgeBaseId) {
      this.setData({ selectedKnowledgeBaseId: settings.selectedKnowledgeBaseId });
    }
    this.setData({ needsSettings });
    if (needsSettings) {
      return;
    }
    this.loadKnowledgeBases();
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
      selectedKnowledgeBaseName: selected.name
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
      const settings = getSettings();
      const selectedIndex = Math.max(
        0,
        knowledgeBases.findIndex((item) => item.id === settings.selectedKnowledgeBaseId)
      );
      const selected = knowledgeBases[selectedIndex];
      this.setData({
        knowledgeBases,
        knowledgeBaseNames,
        selectedIndex,
        selectedKnowledgeBaseId: selected?.id || "",
        selectedKnowledgeBaseName: selected?.name || "",
        statusMessage: knowledgeBases.length
          ? `Loaded ${knowledgeBases.length} knowledge bases.`
          : "No knowledge bases found."
      });
      if (selected?.id) {
        saveSettings({ selectedKnowledgeBaseId: selected.id });
      }
    } catch (error) {
      wx.showModal({
        title: "Load failed",
        content: error.message,
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
      wx.showToast({ title: "Imported", icon: "success" });
    } catch (error) {
      wx.showModal({
        title: "Import failed",
        content: error.message,
        showCancel: false
      });
    } finally {
      this.setData({ importing: false });
    }
  }
});
