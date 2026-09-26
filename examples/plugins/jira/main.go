// Command jira syncs Jira Cloud issues into WeKnora knowledge bases. It is the
// fullest example plugin:
//
//   - a data source connector with incremental sync, checkpoints and
//     deletions;
//   - an OAuth field (x-oauth): WeKnora runs Atlassian's consent flow with
//     the platform's OAuth app and hands the plugin a fresh access token;
//   - dynamic choices (x-options): the sites an account can reach and the
//     site's issue types;
//   - a skill shipped in the package.
//
// Build a package with ./package.sh; install it under System administration
// → Plugin management.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Version must match plugin.yaml.
const Version = "1.0.0"

func main() {
	if err := newPlugin().Serve(); err != nil {
		log.Fatal(err)
	}
}

func newPlugin() *pluginsdk.Plugin {
	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.jira", Version: Version})
	p.Connector("jira", connector{})
	p.Options("sites", siteOptions)
	p.Options("issue_types", issueTypeOptions)
	registerTools(p)
	p.Webhook("issues", issueWebhook)
	return p
}

// settings are the connector's x-group: settings fields.
type settings struct {
	IssueTypes      []string `json:"issue_types"`
	JQL             string   `json:"jql"`
	IncludeComments *bool    `json:"include_comments"`
}

func (s settings) comments() bool { return s.IncludeComments == nil || *s.IncludeComments }

func decode(cfg pluginsdk.ConnectorConfig) (credentials, settings, error) {
	var cr credentials
	var st settings
	if err := remarshal(cfg.Credentials, &cr); err != nil {
		return cr, st, pluginapi.InvalidConfig("credentials: "+err.Error(), nil)
	}
	if err := remarshal(cfg.Settings, &st); err != nil {
		return cr, st, pluginapi.InvalidConfig("settings: "+err.Error(), nil)
	}
	return cr, st, nil
}

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

type connector struct{}

// Validate signs in to the site.
func (connector) Validate(ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig) error {
	cr, _, err := decode(cfg)
	if err != nil {
		return err
	}
	c, err := newClient(ctx, cr, call.Locale)
	if err != nil {
		return err
	}
	_, err = c.myself(ctx)
	return err
}

// ListResources offers the projects the user can browse.
func (connector) ListResources(
	ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig, parentID string,
) ([]pluginapi.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	cr, _, err := decode(cfg)
	if err != nil {
		return nil, err
	}
	c, err := newClient(ctx, cr, call.Locale)
	if err != nil {
		return nil, err
	}
	projects, err := c.projects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pluginapi.Resource, 0, len(projects))
	for _, p := range projects {
		out = append(out, pluginapi.Resource{
			ExternalID: p.Key, Name: p.Name, Type: "project", Description: p.Key,
			URL: c.siteURL + "/browse/" + p.Key,
		})
	}
	return out, nil
}

// syncState is the cursor: per project, the newest update seen (where the
// next incremental sync starts) and the issues it holds (so a full sync can
// tell which were deleted).
type syncState struct {
	Projects map[string]*projectState `json:"projects"`
}

type projectState struct {
	Since time.Time `json:"since"`
	IDs   []string  `json:"ids"`
}

func stateFrom(c *pluginapi.Cursor) syncState {
	st := syncState{Projects: map[string]*projectState{}}
	if c != nil {
		_ = remarshal(c.State, &st)
		if st.Projects == nil {
			st.Projects = map[string]*projectState{}
		}
	}
	return st
}

func (s syncState) cursor(now time.Time) pluginapi.Cursor {
	var state map[string]any
	_ = remarshal(s, &state)
	return pluginapi.Cursor{LastSyncTime: &now, State: state}
}

// Fetch syncs the selected projects. Issues come oldest update first, so the
// cursor can advance with every page; a full sync also reports the issues
// that disappeared (deleted, moved out, or no longer matching the filter).
func (connector) Fetch(
	ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig, in pluginapi.FetchInput,
	s *pluginsdk.Stream,
) (*pluginapi.Cursor, error) {
	cr, st, err := decode(cfg)
	if err != nil {
		return nil, err
	}
	keys := in.ResourceIDs
	if len(keys) == 0 {
		keys = cfg.ResourceIDs
	}
	if len(keys) == 0 {
		return nil, pluginapi.InvalidConfig(say(call.Locale, msgNoProjects), nil)
	}
	c, err := newClient(ctx, cr, call.Locale)
	if err != nil {
		return nil, err
	}
	me, err := c.myself(ctx)
	if err != nil {
		return nil, err
	}
	// JQL reads dates in the user's time zone.
	loc, err := time.LoadLocation(me.TimeZone)
	if err != nil || me.TimeZone == "" {
		loc = time.UTC
	}

	prev := stateFrom(in.Cursor)
	incremental := in.Mode == pluginapi.FetchIncremental && in.Cursor != nil
	// Checkpoints are complete snapshots: projects not reached yet keep
	// their previous state.
	next := syncState{Projects: map[string]*projectState{}}
	for _, key := range keys {
		if p := prev.Projects[key]; p != nil {
			cp := *p
			next.Projects[key] = &cp
		} else {
			next.Projects[key] = &projectState{}
		}
	}
	seen := map[string]bool{}
	now := time.Now().UTC()

	for _, key := range keys {
		ps := next.Projects[key]
		var since time.Time
		if incremental {
			since = ps.Since
		}
		known := map[string]bool{}
		for _, id := range ps.IDs {
			known[id] = true
		}
		_ = s.Progress("syncing " + key)
		err := c.search(ctx, buildJQL(key, st, since, loc), st.comments(), func(page []issue) error {
			for i := range page {
				is := &page[i]
				if err := s.Item(c.item(is, key)); err != nil {
					return err
				}
				seen[is.ID], known[is.ID] = true, true
				if is.Fields.Updated.After(ps.Since) {
					ps.Since = is.Fields.Updated.Time
				}
			}
			ps.IDs = sortedKeys(known)
			return s.Checkpoint(next.cursor(now))
		})
		if err != nil {
			return nil, err
		}
	}

	if !incremental {
		// Everything was listed: what the last sync had and this one did
		// not see is gone.
		for _, key := range keys {
			var kept []string
			if p := prev.Projects[key]; p != nil {
				for _, id := range p.IDs {
					if seen[id] {
						continue
					}
					gone := pluginapi.FetchedItem{ExternalID: id, IsDeleted: true, SourceResourceID: key}
					if err := s.Item(gone); err != nil {
						return nil, err
					}
				}
			}
			for _, id := range next.Projects[key].IDs {
				if seen[id] {
					kept = append(kept, id)
				}
			}
			next.Projects[key].IDs = kept
		}
	}
	cur := next.cursor(now)
	return &cur, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildJQL is one project's query: the settings' filters, the issues updated
// since the last sync (to the minute, JQL's precision), oldest first.
func buildJQL(key string, st settings, since time.Time, loc *time.Location) string {
	parts := []string{"project = " + quote(key)}
	if len(st.IssueTypes) > 0 {
		types := make([]string, len(st.IssueTypes))
		for i, t := range st.IssueTypes {
			types[i] = quote(t)
		}
		parts = append(parts, "issuetype in ("+strings.Join(types, ", ")+")")
	}
	if extra := strings.TrimSpace(st.JQL); extra != "" {
		parts = append(parts, "("+extra+")")
	}
	if !since.IsZero() {
		parts = append(parts, "updated >= "+quote(since.In(loc).Format("2006/01/02 15:04")))
	}
	return strings.Join(parts, " AND ") + " ORDER BY updated ASC"
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// item turns an issue into a Markdown document.
func (c *client) item(is *issue, projectKey string) pluginapi.FetchedItem {
	f := &is.Fields
	url := c.issueURL(is.Key)
	created, updated := f.Created.Time, f.Updated.Time
	return pluginapi.FetchedItem{
		ExternalID:       is.ID,
		Title:            is.Key + " " + f.Summary,
		Content:          []byte(c.markdown(is)),
		ContentType:      "text/markdown",
		FileName:         fileName(is.Key + " " + f.Summary),
		URL:              url,
		CreatedAt:        nonZero(created),
		UpdatedAt:        nonZero(updated),
		SourceResourceID: projectKey,
		Metadata: map[string]string{
			"channel": "jira", "jira_key": is.Key, "project": projectKey, "status": nameOf(f.Status),
			"issue_type": nameOf(f.IssueType), "assignee": userOf(f.Assignee), "priority": nameOf(f.Priority),
			"labels": strings.Join(f.Labels, ","),
		},
	}
}

func (c *client) issueURL(key string) string { return c.siteURL + "/browse/" + key }

// markdown renders an issue: its facts, description and comments.
func (c *client) markdown(is *issue) string {
	f := &is.Fields
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n\n", is.Key, f.Summary)
	facts := [][2]string{
		{"Type", nameOf(f.IssueType)},
		{"Status", nameOf(f.Status)},
		{"Priority", nameOf(f.Priority)},
		{"Resolution", nameOf(f.Resolution)},
		{"Assignee", userOf(f.Assignee)},
		{"Reporter", userOf(f.Reporter)},
		{"Labels", strings.Join(f.Labels, ", ")},
		{"Created", stamp(f.Created)},
		{"Updated", stamp(f.Updated)},
	}
	if f.Parent != nil {
		facts = append(facts, [2]string{"Parent", f.Parent.Key})
	}
	for _, kv := range facts {
		if kv[1] != "" {
			fmt.Fprintf(&b, "- **%s:** %s\n", kv[0], kv[1])
		}
	}
	fmt.Fprintf(&b, "- **Link:** %s\n", c.issueURL(is.Key))
	if desc := adfToMarkdown(f.Description); desc != "" {
		b.WriteString("\n## Description\n\n" + desc + "\n")
	}
	if f.Comment != nil && len(f.Comment.Comments) > 0 {
		b.WriteString("\n## Comments\n")
		for _, cm := range f.Comment.Comments {
			fmt.Fprintf(&b, "\n### %s · %s\n\n%s\n", userOf(cm.Author), stamp(cm.Created), adfToMarkdown(cm.Body))
		}
		if more := f.Comment.Total - len(f.Comment.Comments); more > 0 {
			fmt.Fprintf(&b, "\n_%d more comments are not included._\n", more)
		}
	}
	return b.String()
}

func nameOf(n *named) string {
	if n == nil {
		return ""
	}
	return n.Name
}

func userOf(u *user) string {
	if u == nil {
		return ""
	}
	return u.DisplayName
}

func stamp(t jiraTime) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04 -0700")
}

func nonZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

const maxFileNameBytes = 200

var fileNameReplacer = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	"\n", " ", "\r", " ", "\t", " ",
)

// fileName makes a title safe as a file name: hostile punctuation replaced,
// at most 200 bytes without splitting a rune.
func fileName(title string) string {
	name := strings.TrimSpace(fileNameReplacer.Replace(title))
	if name == "" {
		return "issue.md"
	}
	for len(name) > maxFileNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name + ".md"
}

// formCredentials are the connection fields of the form being filled: a
// data source's credentials, or the workspace's connection for the tools.
func formCredentials(call *pluginsdk.Call, in pluginapi.OptionsInput) (credentials, error) {
	var cr credentials
	if in.Scope == pluginapi.OptionsScopeTenant {
		return cr, remarshal(call.Config.Tenant, &cr)
	}
	var cfg pluginsdk.ConnectorConfig
	if err := call.DecodeInstance(&cfg); err != nil {
		return cr, err
	}
	cr, _, err := decode(cfg)
	return cr, err
}

// siteOptions lists the Jira sites the connected account granted.
func siteOptions(ctx context.Context, call *pluginsdk.Call, in pluginapi.OptionsInput) ([]pluginapi.Option, error) {
	cr, err := formCredentials(call, in)
	if err != nil {
		return nil, err
	}
	if cr.Account == "" {
		return nil, pluginapi.InvalidConfig(say(call.Locale, msgConnectAccount),
			map[string]string{"credentials.account": "required"})
	}
	sites, err := accessibleSites(ctx, cr.Account)
	if err != nil {
		return nil, err
	}
	out := make([]pluginapi.Option, 0, len(sites))
	for _, s := range sites {
		out = append(out, pluginapi.Option{Value: s.ID, Label: s.Name, Description: s.URL})
	}
	return out, nil
}

// issueTypeOptions lists the site's issue types by name (JQL matches names).
func issueTypeOptions(
	ctx context.Context,
	call *pluginsdk.Call,
	in pluginapi.OptionsInput,
) ([]pluginapi.Option, error) {
	cr, err := formCredentials(call, in)
	if err != nil {
		return nil, err
	}
	c, err := newClient(ctx, cr, call.Locale)
	if err != nil {
		return nil, err
	}
	types, err := c.issueTypes(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, t := range types {
		if !slices.Contains(names, t.Name) {
			names = append(names, t.Name)
		}
	}
	sort.Strings(names)
	out := make([]pluginapi.Option, len(names))
	for i, n := range names {
		out[i] = pluginapi.Option{Value: n}
	}
	return out, nil
}
