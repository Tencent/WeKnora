const { saveSettings, getSettings } = require("../../utils/config");
const { createKnowledgeFromURL, listKnowledgeBases } = require("../../utils/request");

Page({
  data: {
    importing: false,
    knowledgeBases: [],
    loading: false,
    selectedIndex: 0,
    selectedKnowledgeBaseId: "",
    selectedKnowledgeBaseName: "",
    url: ""
  },

  onShow() {
    const settings = getSettings();
    if (settings.selectedKnowledgeBaseId) {
      this.setData({ selectedKnowledgeBaseId: settings.selectedKnowledgeBaseId });
    }
    this.loadKnowledgeBases();
  },

  onUrlInput(event) {
    this.setData({ url: event.detail.value });
  },

  onKnowledgeBaseChange(event) {
    const selectedIndex = Number(event.detail.value);
    const selected = this.data.knowledgeBases[selectedIndex];
    if (!selected) return;

    saveSettings({ selectedKnowledgeBaseId: selected.id });
    this.setData({
      selectedIndex,
      selectedKnowledgeBaseId: selected.id,
      selectedKnowledgeBaseName: selected.name
    });
  },

  async loadKnowledgeBases() {
    this.setData({ loading: true });
    try {
      const response = await listKnowledgeBases();
      const knowledgeBases = response.data || [];
      const settings = getSettings();
      const selectedIndex = Math.max(
        0,
        knowledgeBases.findIndex((item) => item.id === settings.selectedKnowledgeBaseId)
      );
      const selected = knowledgeBases[selectedIndex];
      this.setData({
        knowledgeBases,
        selectedIndex,
        selectedKnowledgeBaseId: selected?.id || "",
        selectedKnowledgeBaseName: selected?.name || ""
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
