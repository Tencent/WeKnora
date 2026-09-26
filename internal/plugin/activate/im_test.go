package activate

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

const zulipManifest = `schemaVersion: 1
id: acme.zulip
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: Zulip }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: bin/zulip }
contributes:
  imChannels:
    - id: zulip
      name: { en-US: Zulip, zh-CN: Zulip 聊天 }
      instanceSchema: schemas/channel.json
`

type zulip struct {
	mu   sync.Mutex
	sent []pluginapi.IMSendInput
}

func (z *zulip) Callback(
	_ context.Context, call *pluginsdk.Call, req pluginapi.WebhookRequest,
) (pluginapi.IMCallbackOutput, error) {
	if call.Config.Instance["token"] != "s3cret" || call.TenantID != 7 {
		return pluginapi.IMCallbackOutput{Response: &pluginapi.WebhookResponse{Status: http.StatusUnauthorized}}, nil
	}
	var body struct{ Challenge, Text, From string }
	_ = json.Unmarshal(req.Body, &body)
	if body.Challenge != "" {
		return pluginapi.IMCallbackOutput{Response: &pluginapi.WebhookResponse{
			ContentType: "text/plain", Body: []byte(body.Challenge),
		}}, nil
	}
	return pluginapi.IMCallbackOutput{Message: &pluginapi.IMMessage{
		UserID: body.From, ChatID: "stream-1", ChatType: "group", Content: body.Text, MessageID: "m1",
	}}, nil
}

func (z *zulip) Send(_ context.Context, _ *pluginsdk.Call, in pluginapi.IMSendInput) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.sent = append(z.sent, in)
	return nil
}

func TestPluginIMChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	z := &zulip{}
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.zulip", Version: "1.0.0"})
	plugin.IMChannel("zulip", z)
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()
	p, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml": zulipManifest, "bin/zulip": "x",
		"schemas/channel.json": `{"type":"object","properties":{"token":{"type":"string","x-secret":true}}}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(p.Manifest); err != nil {
		t.Fatal(err)
	}
	platforms := NewIMPlatforms(NewInvoker(fakeClients{client.New(srv.URL, nil, nil)}), reg)

	list := platforms.Platforms()
	if len(list) != 1 || list[0].ID != "acme.zulip/zulip" || list[0].Names["zh-CN"] != "Zulip 聊天" ||
		list[0].Modes[0] != im.ModeWebhook || list[0].ConfigSchema == nil ||
		!list[0].ConfigSchema.Properties["token"].Secret {
		t.Fatalf("platforms = %+v", list)
	}
	_, factory, ok := platforms.Platform("acme.zulip/zulip")
	if !ok {
		t.Fatal("platform not found")
	}
	got := make(chan *im.IncomingMessage, 1)
	channel := &im.IMChannel{ID: "ch1", TenantID: 7, Credentials: types.JSON(`{"token":"s3cret"}`)}
	adapter, _, err := factory(context.Background(), channel, func(_ context.Context, m *im.IncomingMessage) error {
		got <- m
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	callback := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/ch1", bytes.NewBufferString(body))
		if !adapter.HandleURLVerification(c) {
			t.Fatal("the plugin adapter must take the whole callback")
		}
		return w
	}
	if w := callback(`{"challenge":"abc"}`); w.Code != 200 || w.Body.String() != "abc" {
		t.Fatalf("challenge = %d %q", w.Code, w.Body.String())
	}
	if w := callback(`{"text":"hello","from":"u1"}`); w.Code != 200 || w.Body.String() != "{}" {
		t.Fatalf("message ack = %d %q", w.Code, w.Body.String())
	}
	var msg *im.IncomingMessage
	select {
	case msg = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the message was not handled")
	}
	if msg.Platform != "acme.zulip/zulip" || msg.Content != "hello" || msg.ChatType != im.ChatTypeGroup {
		t.Fatalf("message = %+v", msg)
	}
	reply := &im.ReplyMessage{Content: "hi!", IsFinal: true}
	if err := adapter.SendReply(context.Background(), msg, reply); err != nil {
		t.Fatal(err)
	}
	if len(z.sent) != 1 || z.sent[0].Content != "hi!" || z.sent[0].Message.MessageID != "m1" {
		t.Fatalf("sent = %+v", z.sent)
	}

	channel.Credentials = types.JSON(`{"token":"wrong"}`)
	if w := callback(`{"text":"hello","from":"u1"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d", w.Code)
	}
}

// versionClients records the plugin version each call was made for.
type versionClients struct {
	c        *client.Client
	versions *[]string
}

func (f versionClients) Client(_ context.Context, m *manifest.Manifest) (*client.Client, error) {
	*f.versions = append(*f.versions, m.Version)
	return f.c, nil
}

func (f versionClients) OnThisNode(string) bool { return true }

// A channel keeps running across a plugin upgrade and must call the version
// that is loaded now, not the one its adapter was built with.
func TestPluginIMChannelFollowsUpgrades(t *testing.T) {
	plugin := pluginsdk.New(pluginsdk.Info{ID: "acme.zulip", Version: "1.0.0"})
	plugin.IMChannel("zulip", &zulip{})
	srv := httptest.NewServer(plugin.Handler())
	defer srv.Close()
	p, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml": zulipManifest, "bin/zulip": "x", "schemas/channel.json": `{"type":"object"}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(p.Manifest); err != nil {
		t.Fatal(err)
	}
	var versions []string
	platforms := NewIMPlatforms(NewInvoker(versionClients{client.New(srv.URL, nil, nil), &versions}), reg)
	_, factory, _ := platforms.Platform("acme.zulip/zulip")
	channel := &im.IMChannel{ID: "ch1", TenantID: 7, Credentials: types.JSON(`{"token":"s3cret"}`)}
	adapter, _, err := factory(context.Background(), channel, nil)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := *p.Manifest
	upgraded.Version = "2.0.0"
	if err := reg.Replace(&upgraded); err != nil {
		t.Fatal(err)
	}
	msg := &im.IncomingMessage{UserID: "u1", ChatID: "c"}
	if err := adapter.SendReply(context.Background(), msg, &im.ReplyMessage{Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != "2.0.0" {
		t.Fatalf("called versions %v, want the upgraded 2.0.0", versions)
	}
	if err := reg.Unregister("acme.zulip"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SendReply(context.Background(), msg, &im.ReplyMessage{Content: "hi"}); err == nil {
		t.Fatal("a channel of an unloaded plugin must fail")
	}
}
