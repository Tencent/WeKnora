package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// issueWebhook receives Jira's issue events and syncs the data sources that
// follow the issue's project, so changes arrive in minutes, not at the next
// scheduled sync. An incremental sync lists updated issues only, so a
// deleted issue also leaves a note it reads (see deletionNotes).
func issueWebhook(
	ctx context.Context,
	call *pluginsdk.Call,
	req pluginapi.WebhookRequest,
) (*pluginapi.WebhookResponse, error) {
	if secret, _ := call.Config.Tenant["webhook_secret"].(string); secret != "" && !validSignature(secret, req) {
		return &pluginapi.WebhookResponse{Status: http.StatusUnauthorized}, nil
	}
	var ev struct {
		WebhookEvent string `json:"webhookEvent"`
		Issue        struct {
			ID     string `json:"id"`
			Key    string `json:"key"`
			Fields struct {
				Project struct {
					Key string `json:"key"`
				} `json:"project"`
			} `json:"fields"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(req.Body, &ev); err != nil || ev.Issue.Key == "" {
		// Not an issue event: acknowledge so Jira does not retry it.
		return &pluginapi.WebhookResponse{Status: http.StatusAccepted}, nil
	}
	project := ev.Issue.Fields.Project.Key
	if project == "" {
		project, _, _ = strings.Cut(ev.Issue.Key, "-")
	}
	host := call.Host()
	if host == nil {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "the Host API is not available")
	}
	if ev.WebhookEvent == "jira:issue_deleted" && issueID.MatchString(ev.Issue.ID) {
		if err := host.KVPut(ctx, deletionPrefix+ev.Issue.ID, ev.Issue.Key, deletionNoteTTL); err != nil {
			return nil, err
		}
	}
	sources, err := host.DataSources(ctx)
	if err != nil {
		return nil, err
	}
	synced := []string{}
	for _, ds := range sources {
		if ds.Connector != "jira" || !slices.Contains(ds.ResourceIDs, project) {
			continue
		}
		if _, err := host.SyncDataSource(ctx, ds.ID); err != nil {
			return nil, err
		}
		synced = append(synced, ds.ID)
	}
	body, _ := json.Marshal(map[string]any{"project": project, "synced": synced})
	return &pluginapi.WebhookResponse{Status: http.StatusAccepted, ContentType: "application/json", Body: body}, nil
}

// Deletion notes are key-value entries "deleted/<issue id>" the webhook
// leaves. Incremental syncs report the issues they name, once Jira confirms
// they are gone: the webhook may be unsigned, so a note alone deletes
// nothing. Notes expire; a full sync finds whatever they missed.
const (
	deletionPrefix  = "deleted/"
	deletionNoteTTL = 7 * 24 * time.Hour
)

var issueID = regexp.MustCompile(`^[0-9]{1,20}$`)

// deletionNotes are the issue IDs the webhook noted deleted. Without the
// Host API, or when it fails, there are none: a full sync catches up.
func deletionNotes(ctx context.Context, call *pluginsdk.Call) map[string]bool {
	out := map[string]bool{}
	host := call.Host()
	if host == nil {
		return out
	}
	for after := ""; ; {
		page, err := host.KVList(ctx, deletionPrefix, after, pluginapi.KVMaxListLimit)
		if err != nil {
			return out
		}
		for _, e := range page.Entries {
			out[strings.TrimPrefix(e.Key, deletionPrefix)] = true
		}
		if page.Next == "" {
			return out
		}
		after = page.Next
	}
}

// validSignature checks Jira's X-Hub-Signature: "sha256=" and the HMAC of
// the body with the webhook's secret.
func validSignature(secret string, req pluginapi.WebhookRequest) bool {
	sig := ""
	for k, v := range req.Headers {
		if strings.EqualFold(k, "X-Hub-Signature") {
			sig = v
		}
	}
	got, ok := strings.CutPrefix(sig, "sha256=")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(req.Body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.ToLower(got)), []byte(want))
}
