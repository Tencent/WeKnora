package sandbox

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRemoteHandle struct {
	id                 string
	provider           RemoteProvider
	metadata           map[string]string
	trafficAccessToken string
}

func (h *fakeRemoteHandle) ID() string               { return h.id }
func (h *fakeRemoteHandle) Provider() RemoteProvider { return h.provider }
func (h *fakeRemoteHandle) Metadata() map[string]string {
	return cloneStringMap(h.metadata)
}
func (h *fakeRemoteHandle) TrafficAccessToken() string { return h.trafficAccessToken }

// fakeTokenlessHandle models Docker, whose handle deliberately does not
// implement RemoteInboundTokenCarrier because the backend has no inbound
// credential at all. InboundTokenOf therefore returns "" for a reason that is
// not "the credential was lost".
type fakeTokenlessHandle struct {
	id       string
	provider RemoteProvider
	metadata map[string]string
}

func (h *fakeTokenlessHandle) ID() string               { return h.id }
func (h *fakeTokenlessHandle) Provider() RemoteProvider { return h.provider }
func (h *fakeTokenlessHandle) Metadata() map[string]string {
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

type fakeRemoteWriteFile struct {
	path    string
	content []byte
}

type fakeRemoteClient struct {
	mu           sync.Mutex
	provider     RemoteProvider
	capabilities RemoteSandboxCapabilities
	nextID       int
	sandboxes    map[string]*fakeRemoteRecord

	createCount int
	connectIDs  []string
	connects    []RemoteConnectRequest
	getIDs      []string
	deleteIDs   []string
	listCount   int

	trafficAccessToken string

	// reissuesTokenOnConnect models a provider that hands the inbound token
	// back on reconnect. E2B and Cube issue it only at create time, so the
	// default false is the realistic behaviour.
	reissuesTokenOnConnect bool

	// omitsInboundTokenCarrier makes handles skip
	// RemoteInboundTokenCarrier, the way Docker's do.
	omitsInboundTokenCarrier bool

	createErr   error
	connectErrs map[string]error
	getErrs     map[string]error
	deleteErrs  map[string]error
	listErr     error
	createHook  func(context.Context, RemoteCreateRequest) error
	afterCreate func(RemoteSandboxHandle)
	deleteHook  func(context.Context, string) error

	makeDirPaths        []string
	failMakeDirIfExists bool
	execRequests        []RemoteExecRequest
	writeFiles          []fakeRemoteWriteFile

	// snapshots maps snapshotID -> source sandboxID.
	snapshots   map[string]string
	snapshotSeq int
}

func newFakeRemoteClient(provider RemoteProvider) *fakeRemoteClient {
	client := &fakeRemoteClient{
		provider: provider,
		capabilities: RemoteSandboxCapabilities{
			SupportsReconnect:             true,
			SupportsMetadata:              true,
			SupportsListSandboxes:         true,
			SupportsFilesystemEnumeration: true,
		},
		sandboxes:   make(map[string]*fakeRemoteRecord),
		connectErrs: make(map[string]error),
		getErrs:     make(map[string]error),
		deleteErrs:  make(map[string]error),
	}
	// Cube and E2B issue the inbound token at create. DefaultConfig closes
	// public inbound, so a tokenless fake would fail createAndBind the same
	// way a real provider that omitted the token does. Tests that want that
	// failure must clear trafficAccessToken explicitly.
	if provider == RemoteProvider(SandboxTypeCube) || provider == RemoteProvider(SandboxTypeE2B) {
		client.trafficAccessToken = "test-inbound-token"
	}
	return client
}

func TestFakeRemoteClientConnectRestoresRequestedTrafficToken(t *testing.T) {
	client := newFakeRemoteClient(RemoteProvider(SandboxTypeCube))
	client.trafficAccessToken = "provider-issued-token"

	created, err := client.Create(context.Background(), RemoteCreateRequest{})
	require.NoError(t, err)
	require.Equal(t, "provider-issued-token", InboundTokenOf(created))

	reconnected, err := client.Connect(context.Background(), RemoteConnectRequest{
		SandboxID: created.ID(),
	})
	require.NoError(t, err)
	require.Empty(t, InboundTokenOf(reconnected))

	reconnected, err = client.Connect(context.Background(), RemoteConnectRequest{
		SandboxID:          created.ID(),
		TrafficAccessToken: "restored-token",
	})
	require.NoError(t, err)
	require.Equal(t, "restored-token", InboundTokenOf(reconnected))
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
	handle := c.handle(record, c.trafficAccessToken)
	afterCreate := c.afterCreate
	c.mu.Unlock()
	if afterCreate != nil {
		afterCreate(handle)
	}
	return handle, nil
}

func (c *fakeRemoteClient) Connect(
	ctx context.Context,
	request RemoteConnectRequest,
) (RemoteSandboxHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sandboxID := request.SandboxID
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectIDs = append(c.connectIDs, sandboxID)
	c.connects = append(c.connects, request)
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
	token := request.TrafficAccessToken
	if token == "" && c.reissuesTokenOnConnect {
		token = c.trafficAccessToken
	}
	return c.handle(record, token), nil
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
	_ context.Context,
	_ RemoteSandboxHandle,
	request RemoteExecRequest,
) (*RemoteExecResult, error) {
	c.mu.Lock()
	c.execRequests = append(c.execRequests, request)
	c.mu.Unlock()
	return &RemoteExecResult{ExitCode: 0}, nil
}

func (c *fakeRemoteClient) WriteFile(
	_ context.Context,
	_ RemoteSandboxHandle,
	path string,
	content []byte,
) error {
	c.mu.Lock()
	c.writeFiles = append(c.writeFiles, fakeRemoteWriteFile{
		path:    path,
		content: append([]byte(nil), content...),
	})
	c.mu.Unlock()
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

func (c *fakeRemoteClient) MakeDir(_ context.Context, _ RemoteSandboxHandle, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failMakeDirIfExists {
		for _, existing := range c.makeDirPaths {
			if existing == path {
				return NewRemoteError(
					c.provider, "MakeDir", RemoteErrorKindInternal,
					fmt.Sprintf("failed to make dir %s: directory already exists: %s", path, path),
					nil,
				)
			}
		}
	}
	c.makeDirPaths = append(c.makeDirPaths, path)
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

func (c *fakeRemoteClient) CreateSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshots == nil {
		c.snapshots = map[string]string{}
	}
	c.snapshotSeq++
	id := fmt.Sprintf("snap-%d", c.snapshotSeq)
	c.snapshots[id] = sandboxID
	names := []string(nil)
	if name != "" {
		names = []string{name}
	}
	return RemoteSnapshotRef{ID: id, Names: names}, nil
}

func (c *fakeRemoteClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.snapshots, snapshotID) // missing snapshot is success
	return nil
}

func (c *fakeRemoteClient) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []RemoteSnapshotRef
	for id, src := range c.snapshots {
		if sandboxID != "" && src != sandboxID {
			continue
		}
		out = append(out, RemoteSnapshotRef{ID: id})
	}
	return out, nil
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

func (c *fakeRemoteClient) handle(
	record *fakeRemoteRecord,
	trafficAccessToken string,
) RemoteSandboxHandle {
	if c.omitsInboundTokenCarrier {
		return &fakeTokenlessHandle{
			id:       record.id,
			provider: c.provider,
			metadata: cloneStringMap(record.metadata),
		}
	}
	return &fakeRemoteHandle{
		id:                 record.id,
		provider:           c.provider,
		metadata:           cloneStringMap(record.metadata),
		trafficAccessToken: trafficAccessToken,
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
