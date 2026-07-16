package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type fakeRemoteHandle struct {
	id       string
	provider RemoteProvider
	metadata map[string]string
}

func (h *fakeRemoteHandle) ID() string               { return h.id }
func (h *fakeRemoteHandle) Provider() RemoteProvider { return h.provider }
func (h *fakeRemoteHandle) Metadata() map[string]string {
	return cloneStringMap(h.metadata)
}

type fakeRemoteRecord struct {
	id         string
	templateID string
	state      RemoteSandboxState
	rawState   string
	metadata   map[string]string
	startedAt  time.Time
}

type fakeRemoteClient struct {
	mu           sync.Mutex
	provider     RemoteProvider
	capabilities RemoteSandboxCapabilities
	nextID       int
	sandboxes    map[string]*fakeRemoteRecord

	createCount int
	connectIDs  []string
	getIDs      []string
	deleteIDs   []string
	listCount   int

	createErr   error
	connectErrs map[string]error
	getErrs     map[string]error
	deleteErrs  map[string]error
	listErr     error
	createHook  func(context.Context, RemoteCreateRequest) error
	afterCreate func(RemoteSandboxHandle)
	deleteHook  func(context.Context, string) error
}

func newFakeRemoteClient(provider RemoteProvider) *fakeRemoteClient {
	return &fakeRemoteClient{
		provider: provider,
		capabilities: RemoteSandboxCapabilities{
			SupportsReconnect:     true,
			SupportsMetadata:      true,
			SupportsListSandboxes: true,
		},
		sandboxes:   make(map[string]*fakeRemoteRecord),
		connectErrs: make(map[string]error),
		getErrs:     make(map[string]error),
		deleteErrs:  make(map[string]error),
	}
}

func (c *fakeRemoteClient) Provider() RemoteProvider { return c.provider }

func (c *fakeRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return c.capabilities
}

func (c *fakeRemoteClient) Health(context.Context) error { return nil }

func (c *fakeRemoteClient) Create(
	ctx context.Context,
	req RemoteCreateRequest,
) (RemoteSandboxHandle, error) {
	c.mu.Lock()
	c.createCount++
	err := c.createErr
	hook := c.createHook
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if hook != nil {
		if err := hook(ctx, req); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.nextID++
	id := fmt.Sprintf("%s-%d", c.provider, c.nextID)
	record := &fakeRemoteRecord{
		id:         id,
		templateID: req.TemplateID,
		state:      RemoteStateRunning,
		rawState:   string(RemoteStateRunning),
		metadata:   cloneStringMap(req.Metadata),
		startedAt:  time.Now().UTC(),
	}
	c.sandboxes[id] = record
	handle := c.handle(record)
	afterCreate := c.afterCreate
	c.mu.Unlock()
	if afterCreate != nil {
		afterCreate(handle)
	}
	return handle, nil
}

func (c *fakeRemoteClient) Connect(
	ctx context.Context,
	sandboxID string,
) (RemoteSandboxHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectIDs = append(c.connectIDs, sandboxID)
	if err := c.connectErrs[sandboxID]; err != nil {
		return nil, err
	}
	record := c.sandboxes[sandboxID]
	if record == nil || record.state == RemoteStateTerminal {
		return nil, NewRemoteError(
			c.provider,
			"Connect",
			RemoteErrorKindNotFound,
			"sandbox not found",
			nil,
		)
	}
	return c.handle(record), nil
}

func (c *fakeRemoteClient) Get(
	ctx context.Context,
	sandboxID string,
) (*RemoteSandboxSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getIDs = append(c.getIDs, sandboxID)
	if err := c.getErrs[sandboxID]; err != nil {
		return nil, err
	}
	record := c.sandboxes[sandboxID]
	if record == nil {
		return nil, NewRemoteError(
			c.provider,
			"Get",
			RemoteErrorKindNotFound,
			"sandbox not found",
			nil,
		)
	}
	return c.summary(record), nil
}

func (c *fakeRemoteClient) List(
	ctx context.Context,
	filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listCount++
	if c.listErr != nil {
		return nil, c.listErr
	}
	result := make([]RemoteSandboxSummary, 0)
	for _, record := range c.sandboxes {
		if !metadataContains(record.metadata, filter.Metadata) ||
			!stateMatches(record.state, filter.States) {
			continue
		}
		result = append(result, *c.summary(record))
	}
	return result, nil
}

func (c *fakeRemoteClient) Delete(ctx context.Context, sandboxID string) error {
	c.mu.Lock()
	hook := c.deleteHook
	c.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, sandboxID); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteIDs = append(c.deleteIDs, sandboxID)
	if err := c.deleteErrs[sandboxID]; err != nil {
		return err
	}
	if _, ok := c.sandboxes[sandboxID]; !ok {
		return NewRemoteError(
			c.provider,
			"Delete",
			RemoteErrorKindNotFound,
			"sandbox not found",
			nil,
		)
	}
	delete(c.sandboxes, sandboxID)
	return nil
}

func (c *fakeRemoteClient) Exec(
	context.Context,
	RemoteSandboxHandle,
	RemoteExecRequest,
) (*RemoteExecResult, error) {
	return &RemoteExecResult{ExitCode: 0}, nil
}

func (c *fakeRemoteClient) WriteFile(
	context.Context,
	RemoteSandboxHandle,
	string,
	[]byte,
) error {
	return nil
}

func (c *fakeRemoteClient) ReadFile(
	context.Context,
	RemoteSandboxHandle,
	string,
) ([]byte, error) {
	return nil, nil
}

func (c *fakeRemoteClient) ListDir(
	context.Context,
	RemoteSandboxHandle,
	string,
) ([]RemoteDirEntry, error) {
	return nil, nil
}

func (c *fakeRemoteClient) MakeDir(context.Context, RemoteSandboxHandle, string) error {
	return nil
}

func (c *fakeRemoteClient) Remove(context.Context, RemoteSandboxHandle, string) error {
	return nil
}

func (c *fakeRemoteClient) Stat(
	context.Context,
	RemoteSandboxHandle,
	string,
) (*RemoteStatEntry, error) {
	return nil, nil
}

func (c *fakeRemoteClient) addSandbox(
	id string,
	templateID string,
	state RemoteSandboxState,
	metadata map[string]string,
	startedAt time.Time,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sandboxes[id] = &fakeRemoteRecord{
		id:         id,
		templateID: templateID,
		state:      state,
		rawState:   string(state),
		metadata:   cloneStringMap(metadata),
		startedAt:  startedAt,
	}
}

func (c *fakeRemoteClient) setState(id string, state RemoteSandboxState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if record := c.sandboxes[id]; record != nil {
		record.state = state
		record.rawState = string(state)
	}
}

func (c *fakeRemoteClient) counts() (creates, connects, gets, lists, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createCount, len(c.connectIDs), len(c.getIDs), c.listCount, len(c.deleteIDs)
}

func (c *fakeRemoteClient) hasSandbox(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.sandboxes[id]
	return ok
}

func (c *fakeRemoteClient) handle(record *fakeRemoteRecord) RemoteSandboxHandle {
	return &fakeRemoteHandle{
		id:       record.id,
		provider: c.provider,
		metadata: cloneStringMap(record.metadata),
	}
}

func (c *fakeRemoteClient) summary(record *fakeRemoteRecord) *RemoteSandboxSummary {
	return &RemoteSandboxSummary{
		ID:         record.id,
		TemplateID: record.templateID,
		State:      record.state,
		RawState:   record.rawState,
		Metadata:   cloneStringMap(record.metadata),
		StartedAt:  record.startedAt,
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func metadataContains(candidate, required map[string]string) bool {
	for key, value := range required {
		if candidate[key] != value {
			return false
		}
	}
	return true
}

func stateMatches(candidate RemoteSandboxState, allowed []RemoteSandboxState) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, state := range allowed {
		if candidate == state {
			return true
		}
	}
	return false
}

var _ RemoteSandboxClient = (*fakeRemoteClient)(nil)
