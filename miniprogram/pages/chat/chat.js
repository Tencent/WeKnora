const { getSettings } = require("../../utils/config");
const { createSession, knowledgeChat } = require("../../utils/request");
const { collectAnswerFromSSE } = require("../../utils/sse");
const { applyNavTitle, applyTabBar, getMessages, t } = require("../../utils/i18n");

function buildViewModel() {
  const messages = getMessages();
  return {
    chatTitle: messages.chatTitle,
    chatSubtitle: messages.chatSubtitle,
    questionLabel: messages.question,
    questionPlaceholder: messages.questionPlaceholder,
    askText: messages.askWeKnora,
    answerLabel: messages.answer
  };
}

Page({
  data: {
    chatTitle: "知识问答",
    chatSubtitle: "向当前选中的 WeKnora 知识库提问。",
    questionLabel: "问题",
    questionPlaceholder: "输入你的问题…",
    askText: "向 WeKnora 提问",
    answerLabel: "回答",
    answer: "",
    displayAnswer: "",
    loading: false,
    query: "",
    rawResponse: "",
    sessionId: ""
  },

  applyI18n() {
    this.setData(buildViewModel());
  },

  onLoad() {
    this.applyI18n();
  },

  onShow() {
    try {
      applyTabBar();
      applyNavTitle("navChat");
      this.applyI18n();
    } catch (error) {
      // keep fallback Chinese copy
    }
  },

  onQueryInput(event) {
    this.setData({ query: event.detail.value });
  },

  async ensureSession() {
    if (this.data.sessionId) {
      return this.data.sessionId;
    }

    const settings = getSettings();
    const response = await createSession(settings.selectedKnowledgeBaseId);
    const sessionId = response && response.data ? response.data.id : "";
    if (!sessionId) {
      throw new Error(t("sessionIdMissing"));
    }
    this.setData({ sessionId: sessionId });
    return sessionId;
  },

  async ask() {
    this.setData({ answer: "", rawResponse: "", displayAnswer: "", loading: true });
    try {
      const sessionId = await this.ensureSession();
      const settings = getSettings();
      const response = await knowledgeChat(
        sessionId,
        this.data.query.trim(),
        settings.selectedKnowledgeBaseId
      );
      const rawResponse = typeof response === "string" ? response : JSON.stringify(response);
      const answer = collectAnswerFromSSE(rawResponse);
      const displayAnswer = answer || rawResponse;
      this.setData({
        answer: answer,
        rawResponse: answer ? "" : rawResponse,
        displayAnswer: displayAnswer
      });
    } catch (error) {
      wx.showModal({
        title: t("chatFailed"),
        content: (error && error.message) || "",
        showCancel: false
      });
    } finally {
      this.setData({ loading: false });
    }
  }
});
