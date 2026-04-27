App({
  onLaunch() {
    const settings = wx.getStorageSync("weknora_settings");
    if (!settings) {
      wx.setStorageSync("weknora_settings", {
        baseUrl: "http://localhost:8080",
        apiKey: "",
        selectedKnowledgeBaseId: ""
      });
    }
  }
});
