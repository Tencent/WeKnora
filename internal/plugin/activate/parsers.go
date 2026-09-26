package activate

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// parseTimeout bounds one plugin parse when the caller set no deadline.
const parseTimeout = 10 * time.Minute

// Parsers registers code plugins' document parsers as parser engines under
// their qualified IDs, next to the builtin engines and the docreader's.
type Parsers struct {
	iv *Invoker

	mu         sync.Mutex
	registered map[string][]string
}

// NewParsers creates the parser activator.
func NewParsers(iv *Invoker) *Parsers {
	return &Parsers{iv: iv, registered: map[string][]string{}}
}

// Name implements reconcile.Activator.
func (a *Parsers) Name() string { return "parsers" }

// Activate implements reconcile.Activator: all of a plugin's parsers or none.
func (a *Parsers) Activate(_ context.Context, l *reconcile.Loaded) error {
	var ids []string
	for _, c := range l.Manifest.Contributes[manifest.PointParsers] {
		e := &pluginEngine{
			iv: a.iv, m: l.Manifest, local: c.ID, name: manifest.QualifiedID(l.Manifest.ID, c.ID),
			description: c.Description.Default, names: displayNames(c.Name), fileTypes: c.FileTypes,
		}
		if err := docparser.RegisterPluginEngine(e); err != nil {
			for _, done := range ids {
				docparser.UnregisterPluginEngine(done)
			}
			return fmt.Errorf("parser %s: %w", c.ID, err)
		}
		ids = append(ids, e.name)
	}
	a.mu.Lock()
	a.registered[l.Manifest.ID] = ids
	a.mu.Unlock()
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *Parsers) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	ids := a.registered[pluginID]
	delete(a.registered, pluginID)
	a.mu.Unlock()
	for _, id := range ids {
		docparser.UnregisterPluginEngine(id)
	}
	return nil
}

func displayNames(t manifest.LocalizedText) map[string]string {
	out := map[string]string{}
	for k, v := range t.Locales {
		out[k] = v
	}
	if t.Default != "" {
		out["default"] = t.Default
	}
	return out
}

// pluginEngine is a plugin parser as a docparser engine.
type pluginEngine struct {
	iv          *Invoker
	m           *manifest.Manifest
	local, name string
	description string
	names       map[string]string
	fileTypes   []string
}

var (
	_ docparser.EngineRegistration = (*pluginEngine)(nil)
	_ docparser.PluginEngineInfo   = (*pluginEngine)(nil)
)

func (e *pluginEngine) Name() string                    { return e.name }
func (e *pluginEngine) Description() string             { return e.description }
func (e *pluginEngine) FileTypes(bool) []string         { return e.fileTypes }
func (e *pluginEngine) PluginID() string                { return e.m.ID }
func (e *pluginEngine) DisplayNames() map[string]string { return e.names }
func (e *pluginEngine) CheckAvailable(bool, map[string]string) (bool, string) {
	if _, err := e.iv.clients.Client(context.Background(), e.m); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// NewReader implements docparser.EngineRegistration. deps.Overrides carry
// other engines' credentials and are deliberately not passed on; the
// plugin's own settings travel in its system and tenant configuration.
func (e *pluginEngine) NewReader(context.Context, docparser.ReaderDeps) (interfaces.DocReader, error) {
	return &remoteParser{engine: e}, nil
}

// remoteParser reads one document through the plugin.
type remoteParser struct{ engine *pluginEngine }

// Read implements interfaces.DocReader. A retryable plugin failure (the
// plugin is down, rate limited) is returned as an error so the task is
// retried; any other failure is final and reported in the result.
func (r *remoteParser) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	ctx, cancel := withDefaultTimeout(ctx, parseTimeout)
	defer cancel()
	in := pluginapi.ParseInput{
		FileName: req.FileName, FileType: strings.ToLower(strings.TrimPrefix(req.FileType, ".")),
		Content: req.FileContent, URL: req.URL, Title: req.Title,
	}
	var out pluginapi.ParseOutput
	err := r.engine.iv.Call(ctx, r.engine.m, pluginapi.ParsePath(r.engine.local), nil, in, &out)
	if err != nil {
		var pe *pluginapi.Error
		if errors.As(err, &pe) && !pe.Retryable {
			return &types.ReadResult{Error: err.Error()}, nil
		}
		return nil, err
	}
	res := &types.ReadResult{MarkdownContent: out.Markdown, Metadata: out.Metadata}
	for _, img := range out.Images {
		if img.OriginalRef == "" || len(img.Data) == 0 {
			continue
		}
		res.ImageRefs = append(res.ImageRefs, types.ImageRef{
			Filename: path.Base(img.OriginalRef), OriginalRef: img.OriginalRef,
			MimeType: img.MimeType, ImageData: img.Data,
		})
	}
	return res, nil
}
