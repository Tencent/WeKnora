const assert = require("node:assert/strict");
const test = require("node:test");
const { createKnowledgeFromURL, knowledgeChat, listKnowledgeBases } = require("../../miniprogram/utils/request");
const { collectAnswerFromSSE, parseSSE } = require("../../miniprogram/utils/sse");
const { normalizeBaseUrl } = require("../../miniprogram/utils/config");

test("parseSSE extracts event payloads", () => {
  const events = parseSSE('event: message\ndata: {"content":"hi"}\n\n');

  assert.equal(events.length, 1);
  assert.equal(events[0].event, "message");
  assert.equal(events[0].data, '{"content":"hi"}');
});

test("collectAnswerFromSSE joins answer chunks and skips references", () => {
  const raw = [
    'event: message\ndata: {"response_type":"references","content":"skip","done":false}',
    'event: message\ndata: {"response_type":"answer","content":"Hel","done":false}',
    'event: message\ndata: {"response_type":"answer","content":"lo","done":true}'
  ].join("\n\n");

  assert.equal(collectAnswerFromSSE(raw), "Hello");
});

test("normalizeBaseUrl trims trailing slashes", () => {
  assert.equal(normalizeBaseUrl(" https://example.com/// "), "https://example.com");
});

test("API helpers send WeKnora auth headers", async () => {
  let capturedRequest;
  global.wx = {
    getStorageSync() {
      return {
        apiKey: "sk-test",
        baseUrl: "https://weknora.example.com/",
        selectedKnowledgeBaseId: "kb-1"
      };
    },
    request(options) {
      capturedRequest = options;
      options.success({
        statusCode: 200,
        data: {
          data: []
        }
      });
    }
  };

  await listKnowledgeBases();

  assert.equal(capturedRequest.url, "https://weknora.example.com/api/v1/knowledge-bases");
  assert.equal(capturedRequest.header["X-API-Key"], "sk-test");
  assert.match(capturedRequest.header["X-Request-ID"], /^mp-/);
});

test("URL import helper posts the selected URL payload", async () => {
  let capturedRequest;
  global.wx = {
    getStorageSync() {
      return {
        apiKey: "sk-test",
        baseUrl: "https://weknora.example.com",
        selectedKnowledgeBaseId: "kb-1"
      };
    },
    request(options) {
      capturedRequest = options;
      options.success({
        statusCode: 201,
        data: {
          success: true
        }
      });
    }
  };

  await createKnowledgeFromURL("kb-1", "https://github.com/Tencent/WeKnora", true);

  assert.equal(capturedRequest.method, "POST");
  assert.equal(capturedRequest.url, "https://weknora.example.com/api/v1/knowledge-bases/kb-1/knowledge/url");
  assert.deepEqual(capturedRequest.data, {
    url: "https://github.com/Tencent/WeKnora",
    enable_multimodel: true
  });
});

test("chat helper includes selected knowledge base ids", async () => {
  let capturedRequest;
  global.wx = {
    getStorageSync() {
      return {
        apiKey: "sk-test",
        baseUrl: "https://weknora.example.com"
      };
    },
    request(options) {
      capturedRequest = options;
      options.success({
        statusCode: 200,
        data: "event: message\ndata: {}\n\n"
      });
    }
  };

  await knowledgeChat("session-1", "hello", "kb-1");

  assert.equal(capturedRequest.method, "POST");
  assert.equal(capturedRequest.url, "https://weknora.example.com/api/v1/knowledge-chat/session-1");
  assert.deepEqual(capturedRequest.data, {
    query: "hello",
    knowledge_base_ids: ["kb-1"]
  });
});

test("knowledge page skips API loading until settings are configured", async () => {
  const calls = [];
  const pageDefinitions = [];
  const originalPage = global.Page;
  const originalWx = global.wx;

  try {
    global.Page = (definition) => {
      pageDefinitions.push(definition);
    };
    global.wx = {
      getStorageSync() {
        return {};
      },
      request() {
        calls.push("request");
      },
      switchTab() {}
    };

    delete require.cache[require.resolve("../../miniprogram/pages/index/index.js")];
    require("../../miniprogram/pages/index/index.js");
    const page = {
      data: { ...pageDefinitions[0].data },
      setData(nextData) {
        this.data = { ...this.data, ...nextData };
      }
    };

    await pageDefinitions[0].onShow.call(page);

    assert.equal(page.data.needsSettings, true);
    assert.deepEqual(calls, []);
  } finally {
    global.Page = originalPage;
    global.wx = originalWx;
  }
});

test("knowledge page maps API results to picker labels", async () => {
  const pageDefinitions = [];
  const originalPage = global.Page;
  const originalWx = global.wx;
  let savedSettings;

  try {
    global.Page = (definition) => {
      pageDefinitions.push(definition);
    };
    global.wx = {
      getStorageSync() {
        return {
          apiKey: "sk-test",
          baseUrl: "https://weknora.example.com"
        };
      },
      request(options) {
        options.success({
          statusCode: 200,
          data: {
            data: [
              { id: "kb-1", name: "Compliance KB" },
              { id: "kb-2", name: "Docs KB" }
            ]
          }
        });
      },
      setStorageSync(key, value) {
        savedSettings = { key, value };
      },
      switchTab() {}
    };

    delete require.cache[require.resolve("../../miniprogram/pages/index/index.js")];
    require("../../miniprogram/pages/index/index.js");
    const page = {
      data: { ...pageDefinitions[0].data },
      setData(nextData) {
        this.data = { ...this.data, ...nextData };
      }
    };

    await pageDefinitions[0].loadKnowledgeBases.call(page);

    assert.deepEqual(page.data.knowledgeBaseNames, ["Compliance KB", "Docs KB"]);
    assert.equal(page.data.selectedKnowledgeBaseId, "kb-1");
    assert.equal(page.data.selectedKnowledgeBaseName, "Compliance KB");
    assert.equal(savedSettings.value.selectedKnowledgeBaseId, "kb-1");
  } finally {
    global.Page = originalPage;
    global.wx = originalWx;
  }
});
