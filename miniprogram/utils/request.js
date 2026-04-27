const { getSettings } = require("./config");

function request(path, options = {}) {
  const settings = getSettings();
  if (!settings.baseUrl) {
    return Promise.reject(new Error("Please configure the WeKnora API base URL first."));
  }
  if (!settings.apiKey) {
    return Promise.reject(new Error("Please configure the WeKnora API key first."));
  }

  return new Promise((resolve, reject) => {
    wx.request({
      url: `${settings.baseUrl}${path}`,
      method: options.method || "GET",
      data: options.data,
      header: {
        "Content-Type": "application/json",
        "X-API-Key": settings.apiKey,
        "X-Request-ID": `mp-${Date.now()}-${Math.random().toString(16).slice(2)}`
      },
      success(response) {
        if (response.statusCode >= 200 && response.statusCode < 300) {
          resolve(response.data);
          return;
        }
        const message = response.data?.error?.message || response.data?.message || `HTTP ${response.statusCode}`;
        reject(new Error(message));
      },
      fail(error) {
        reject(new Error(error.errMsg || "Network request failed."));
      }
    });
  });
}

function listKnowledgeBases() {
  return request("/api/v1/knowledge-bases");
}

function createKnowledgeFromURL(knowledgeBaseId, url, enableMultimodel = false) {
  return request(`/api/v1/knowledge-bases/${knowledgeBaseId}/knowledge/url`, {
    method: "POST",
    data: {
      url,
      enable_multimodel: enableMultimodel
    }
  });
}

function createSession(knowledgeBaseId) {
  return request("/api/v1/sessions", {
    method: "POST",
    data: knowledgeBaseId ? { knowledge_base_id: knowledgeBaseId } : {}
  });
}

function knowledgeChat(sessionId, query) {
  return request(`/api/v1/knowledge-chat/${sessionId}`, {
    method: "POST",
    data: { query }
  });
}

module.exports = {
  createKnowledgeFromURL,
  createSession,
  knowledgeChat,
  listKnowledgeBases,
  request
};
