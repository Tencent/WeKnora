const assert = require("node:assert/strict");
const test = require("node:test");
const { createKnowledgeFromURL, listKnowledgeBases } = require("../../miniprogram/utils/request");
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
