const { getSettings, saveSettings } = require("../../utils/config");

Page({
  data: {
    baseUrl: "",
    apiKey: ""
  },

  onShow() {
    const settings = getSettings();
    this.setData({
      baseUrl: settings.baseUrl,
      apiKey: settings.apiKey
    });
  },

  onBaseUrlInput(event) {
    this.setData({ baseUrl: event.detail.value });
  },

  onApiKeyInput(event) {
    this.setData({ apiKey: event.detail.value });
  },

  save() {
    saveSettings({
      baseUrl: this.data.baseUrl,
      apiKey: this.data.apiKey
    });
    wx.showToast({ title: "Saved", icon: "success" });
  }
});
