package events

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

type queue struct {
	mu    sync.Mutex
	tasks []*asynq.Task
}

func (q *queue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{}, nil
}

func (q *queue) take() []*asynq.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.tasks
	q.tasks = nil
	return out
}

type oneClient struct{ c *client.Client }

func (o oneClient) Client(context.Context, *manifest.Manifest) (*client.Client, error) {
	return o.c, nil
}
func (o oneClient) OnThisNode(string) bool { return true }

type received struct {
	mu     sync.Mutex
	events []pluginapi.EventDelivery
	tenant []uint64
}

func setup(t *testing.T) (*Dispatcher, *queue, *received, *plugintest.MemTenantSettings) {
	t.Helper()
	reg := registry.New()
	plugin := func(id string, events ...string) *manifest.Manifest {
		return &manifest.Manifest{
			SchemaVersion: manifest.SchemaVersion, ID: id, Version: "1.0.0", APIVersion: pluginapi.APIVersion,
			Name: manifest.Text(id, nil), Publisher: manifest.Publisher{ID: "acme"},
			Runtime:     manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/x"},
			Permissions: manifest.Permissions{Events: events},
		}
	}
	listener := plugin("acme.audit", pluginapi.EventKnowledgeIngested, pluginapi.EventKnowledgeFailed,
		pluginapi.EventKnowledgeDeleted)
	other := plugin("acme.other", pluginapi.EventChatAnswered)
	for _, m := range []*manifest.Manifest{listener, other} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	settings := &plugintest.MemTenantSettings{}
	ctx := context.Background()
	for _, id := range []string{"acme.audit", "acme.other"} {
		_ = settings.Upsert(ctx, &types.PluginTenantSetting{TenantID: 7, PluginID: id, Enabled: true})
	}

	got := &received{}
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.audit", Version: "1.0.0"})
	p.OnEvent(func(_ context.Context, call *pluginsdk.Call, ev pluginapi.EventDelivery) error {
		got.mu.Lock()
		got.events = append(got.events, ev)
		got.tenant = append(got.tenant, call.TenantID)
		got.mu.Unlock()
		var data pluginapi.KnowledgeEventData
		_ = json.Unmarshal(ev.Data, &data)
		switch data.KnowledgeID {
		case "down":
			return pluginapi.Errorf(pluginapi.CodeUnavailable, "try later")
		case "bad":
			return pluginapi.Errorf(pluginapi.CodeBadRequest, "never")
		}
		return nil
	})
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	q := &queue{}
	d := NewDispatcher(reg, tenancy.NewService(reg, settings), q,
		activate.NewInvoker(oneClient{client.New(srv.URL, nil, nil)}))
	return d, q, got, settings
}

func TestPublishFansOutToSubscribedEnabledPlugins(t *testing.T) {
	ctx := context.Background()
	d, q, got, settings := setup(t)

	d.Publish(ctx, 7, pluginapi.EventKnowledgeIngested, pluginapi.KnowledgeEventData{KnowledgeID: "k1"})
	tasks := q.take()
	if len(tasks) != 1 || tasks[0].Type() != TaskType {
		t.Fatalf("tasks = %d", len(tasks))
	}
	if err := d.Handle(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	if len(got.events) != 1 || got.events[0].Type != pluginapi.EventKnowledgeIngested || got.tenant[0] != 7 ||
		got.events[0].Attempt != 1 || got.events[0].ID == "" {
		t.Fatalf("delivered = %+v tenants %v", got.events, got.tenant)
	}

	// Another workspace never switched the plugins on.
	d.Publish(ctx, 8, pluginapi.EventKnowledgeIngested, pluginapi.KnowledgeEventData{KnowledgeID: "k1"})
	if n := len(q.take()); n != 0 {
		t.Fatalf("an off plugin got %d deliveries", n)
	}

	// A plugin switched off after the event was queued does not get it.
	d.Publish(ctx, 7, pluginapi.EventKnowledgeIngested, pluginapi.KnowledgeEventData{KnowledgeID: "k2"})
	queued := q.take()
	_ = settings.Upsert(ctx, &types.PluginTenantSetting{TenantID: 7, PluginID: "acme.audit", Enabled: false})
	if err := d.Handle(ctx, queued[0]); err != nil || len(got.events) != 1 {
		t.Fatalf("delivered to a switched-off plugin: %v", err)
	}
}

func TestHandleRetriesOnlyRetryableFailures(t *testing.T) {
	ctx := context.Background()
	d, q, got, _ := setup(t)
	d.Publish(ctx, 7, pluginapi.EventKnowledgeFailed, pluginapi.KnowledgeEventData{KnowledgeID: "down"})
	d.Publish(ctx, 7, pluginapi.EventKnowledgeFailed, pluginapi.KnowledgeEventData{KnowledgeID: "bad"})
	tasks := q.take()
	if err := d.Handle(ctx, tasks[0]); err == nil {
		t.Fatal("a retryable failure must be retried")
	}
	if err := d.Handle(ctx, tasks[0]); err == nil || got.events[0].ID != got.events[1].ID {
		t.Fatal("a retry keeps the event ID")
	}
	if err := d.Handle(ctx, tasks[1]); err != nil {
		t.Fatalf("a refused event is dropped, got %v", err)
	}
}

// fakeKnowledge is the part of the repository the watch uses.
type fakeKnowledge struct {
	interfaces.KnowledgeRepository
	rows map[string]*types.Knowledge
}

func (f *fakeKnowledge) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	k := *f.rows[id]
	return &k, nil
}

func (f *fakeKnowledge) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	c := *k
	f.rows[k.ID] = &c
	return nil
}

func (f *fakeKnowledge) FinalizeSubtask(_ context.Context, id string) (int, bool, error) {
	f.rows[id].ParseStatus = types.ParseStatusCompleted
	return 0, true, nil
}

func TestWatchKnowledgePublishesTransitionsOnly(t *testing.T) {
	ctx := context.Background()
	d, q, _, _ := setup(t)
	SetDefault(d)
	t.Cleanup(func() { SetDefault(nil) })
	base := &fakeKnowledge{rows: map[string]*types.Knowledge{
		"k1": {ID: "k1", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing},
		"k2": {ID: "k2", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusFinalizing},
	}}
	repo := WatchKnowledge(base)

	k := *base.rows["k1"]
	k.ParseStatus, k.ErrorMessage = types.ParseStatusFailed, "boom"
	_ = repo.UpdateKnowledge(ctx, &k)
	_ = repo.UpdateKnowledge(ctx, &k) // already failed: no second event
	k.ParseStatus = types.ParseStatusProcessing
	_ = repo.UpdateKnowledge(ctx, &k) // not terminal
	_, _, _ = repo.FinalizeSubtask(ctx, "k2")

	var seen []string
	for _, task := range q.take() {
		var p payload
		_ = json.Unmarshal(task.Payload(), &p)
		var data pluginapi.KnowledgeEventData
		_ = json.Unmarshal(p.Event.Data, &data)
		seen = append(seen, p.Event.Type+":"+data.KnowledgeID+":"+data.Error)
	}
	want := []string{"knowledge.failed:k1:boom", "knowledge.ingested:k2:"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("published %v, want %v", seen, want)
	}
}

// Bulk paths publish through the helpers: deleting a whole knowledge base,
// and statuses set outside the watched repository methods.
func TestPublishHelpers(t *testing.T) {
	ctx := context.Background()
	d, q, _, _ := setup(t)
	SetDefault(d)
	t.Cleanup(func() { SetDefault(nil) })
	PublishDeleted(ctx, 7, []*types.Knowledge{
		{ID: "a", KnowledgeBaseID: "kb", Title: "A"}, nil, {ID: "b", KnowledgeBaseID: "kb"},
	})
	stalled := &types.Knowledge{ID: "c", TenantID: 7, ParseStatus: types.ParseStatusFailed, ErrorMessage: "stalled"}
	PublishKnowledge(ctx, stalled)
	PublishKnowledge(ctx, &types.Knowledge{ID: "d", TenantID: 7, ParseStatus: types.ParseStatusFinalizing})
	var seen []string
	for _, task := range q.take() {
		var p payload
		_ = json.Unmarshal(task.Payload(), &p)
		var data pluginapi.KnowledgeEventData
		_ = json.Unmarshal(p.Event.Data, &data)
		seen = append(seen, p.Event.Type+":"+data.KnowledgeID+":"+data.Title+data.Error)
	}
	want := "knowledge.deleted:a:A knowledge.deleted:b: knowledge.failed:c:stalled"
	if strings.Join(seen, " ") != want {
		t.Fatalf("published %v, want %s", seen, want)
	}
}

// Rows failed before plugins load (the startup reset) are published once
// they are.
func TestDeferredKnowledgeWaitsForPlugins(t *testing.T) {
	ctx := context.Background()
	early := &types.Knowledge{ID: "early", TenantID: 7, ParseStatus: types.ParseStatusFailed, ErrorMessage: "restart"}
	DeferKnowledge(early)
	d, q, _, _ := setup(t)
	SetDefault(d)
	t.Cleanup(func() { SetDefault(nil) })
	if len(q.take()) != 0 {
		t.Fatal("published before the flush")
	}
	FlushDeferred(ctx)
	FlushDeferred(ctx)
	if tasks := q.take(); len(tasks) != 1 {
		t.Fatalf("flushed %d events, want 1", len(tasks))
	}
}

// Every runtime with code takes events, kubernetes deployments included.
func TestSubscribedNeedsCode(t *testing.T) {
	for rt, want := range map[manifest.RuntimeType]bool{
		manifest.RuntimeHost: true, manifest.RuntimeRemote: true, manifest.RuntimeKubernetes: true,
		manifest.RuntimeDeclarative: false, manifest.RuntimeBuiltin: false,
	} {
		m := &manifest.Manifest{
			Runtime:     manifest.Runtime{Type: rt},
			Permissions: manifest.Permissions{Events: []string{pluginapi.EventChatAnswered}},
		}
		if got := subscribed(m, pluginapi.EventChatAnswered); got != want {
			t.Errorf("%s: subscribed = %v, want %v", rt, got, want)
		}
	}
}

func TestPublishAnswer(t *testing.T) {
	d, q, _, _ := setup(t)
	SetDefault(d)
	t.Cleanup(func() { SetDefault(nil) })
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")
	PublishAnswer(ctx, &types.Message{ID: "m", SessionID: "s", AgentID: "a", Content: "42"}, "why?")
	tasks := q.take()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	var p payload
	_ = json.Unmarshal(tasks[0].Payload(), &p)
	var data pluginapi.ChatEventData
	_ = json.Unmarshal(p.Event.Data, &data)
	if p.PluginID != "acme.other" || p.Event.Type != pluginapi.EventChatAnswered ||
		data != (pluginapi.ChatEventData{
			SessionID: "s", MessageID: "m", AgentID: "a", UserID: "u1", Question: "why?", Answer: "42",
		}) {
		t.Fatalf("published %+v %+v", p, data)
	}
}

// Deliveries go to the queue the runtime-queues page shows them in.
func TestDeliveriesUseTheDeclaredQueue(t *testing.T) {
	if q, ok := types.QueueForTaskType(TaskType); !ok || q != types.QueueMaintenance {
		t.Fatalf("plugin events are enqueued on %q but declared on %q", types.QueueMaintenance, q)
	}
}
