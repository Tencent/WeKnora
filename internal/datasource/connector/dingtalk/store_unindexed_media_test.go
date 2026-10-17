package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// videoExtensionsUnderTest is every container the connector treats as video.
// Each of them must stay skipped while store_unindexed_media is off.
var videoExtensionsUnderTest = []string{"mp4", "mov", "avi", "mkv", "flv", "wmv", "m4v", "webm"}

// videoArchiveFixture is uploadedDocumentsFixture with the bytes a real 钉盘
// object store would return for the .mp4 node.
func videoArchiveFixture() *fakeAPI {
	api := uploadedDocumentsFixture()
	for _, nodes := range api.nodes {
		for _, candidate := range nodes {
			if candidate.ID == "video" {
				api.downloads["video"] = []byte("\x00\x00\x00\x18ftypmp42-training-video")
			}
		}
	}
	return api
}

// testConfigWithStoredMedia turns only store_unindexed_media on. Uploaded
// Office/PDF files have their own switch and must stay out of this sync.
func testConfigWithStoredMedia(resources ...string) *types.DataSourceConfig {
	cfg := testConfig(resources...)
	cfg.Settings = map[string]interface{}{"store_unindexed_media": true}
	return cfg
}

// With the switch off nothing changes: every video extension keeps the plain
// video reason. With it on the same nodes stop being skips, which is what lets
// the scan treat them as documents.
func TestSkipReasonHonoursTheUnindexedMediaSwitch(t *testing.T) {
	const mediaReason = "video files are deliberately not downloaded by this connector"
	for _, extension := range videoExtensionsUnderTest {
		t.Run(extension, func(t *testing.T) {
			video := node{Type: "FILE", Category: "DOCUMENT", Extension: strings.ToUpper(extension)}
			if got := skipReason(video, documentSettings{}); got != mediaReason {
				t.Fatalf("skipReason(off) = %q, want %q", got, mediaReason)
			}
			if got := skipReason(video, documentSettings{StoreUnindexedMedia: false}); got != mediaReason {
				t.Fatalf("skipReason(explicit off) = %q, want %q", got, mediaReason)
			}
			if got := skipReason(video, documentSettings{StoreUnindexedMedia: true}); got != "" {
				t.Fatalf("skipReason(on) = %q, want no skip reason", got)
			}
		})
	}

	// A node the listing labels VIDEO is admitted by the same rule.
	byCategory := node{Type: "FILE", Category: "VIDEO", Extension: "mp4"}
	if got := skipReason(byCategory, documentSettings{}); got != mediaReason {
		t.Fatalf("video category with the switch off = %q, want %q", got, mediaReason)
	}
	if got := skipReason(byCategory, documentSettings{StoreUnindexedMedia: true}); got != "" {
		t.Fatalf("video category with the switch on = %q, want no skip reason", got)
	}
}

// Enabling the video switch must not open the uploaded-file path, and the
// video rule must not leak into formats the video switch does not govern.
func TestUnindexedMediaSwitchIsScopedToVideoFiles(t *testing.T) {
	settings := documentSettings{StoreUnindexedMedia: true}
	for _, extension := range []string{"mp3", "wav", "m4a", "flac", "aac"} {
		if isVideoExtension(extension) {
			t.Fatalf("%q is audio, not video", extension)
		}
		audio := node{Type: "FILE", Category: "DOCUMENT", Extension: extension}
		if audio.isArchivedVideoFile(settings) {
			t.Fatalf("audio node %q was admitted as an archived video", extension)
		}
	}

	uploaded := node{Type: "FILE", Category: "DOCUMENT", Extension: "docx"}
	if uploaded.isIngestible(settings) {
		t.Fatal("the video switch admitted an uploaded docx")
	}
	if got := skipReason(uploaded, settings); !strings.Contains(got, "include_uploaded_files") {
		t.Fatalf("docx skip reason = %q, want the uploaded-files switch to be named", got)
	}

	// A node without a download path cannot be archived: there is no body to
	// keep, so admitting it would only produce a failed sync item.
	notAFile := node{Type: "DOCUMENT", Category: "VIDEO", Extension: "mp4"}
	if notAFile.isArchivedVideoFile(settings) {
		t.Fatal("a non-FILE node was admitted as an archived video")
	}
}

// With the switch on, a video is downloaded and handed over as an archived item:
// the bytes are kept, the file name keeps its extension, and the item is marked
// so ingestion stores it without dispatching a parse task. Everything else in
// the same sync stays untouched.
func TestFetchAllArchivesVideosWhenTheSwitchIsOn(t *testing.T) {
	api := videoArchiveFixture()
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfigWithStoredMedia("space"), []string{"space"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}

	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	video, ok := byID["video"]
	if !ok {
		t.Fatalf("the video was not synced with the switch on: %#v", items)
	}
	if !video.StoreWithoutIndexing {
		t.Fatalf("video item = %#v, want StoreWithoutIndexing", video)
	}
	if want := []byte("\x00\x00\x00\x18ftypmp42-training-video"); !bytes.Equal(video.Content, want) {
		t.Fatalf("video content = %q, want the downloaded bytes %q", video.Content, want)
	}
	if video.FileName != "Lesson.mp4" {
		t.Fatalf("video file name = %q, want %q", video.FileName, "Lesson.mp4")
	}
	if video.ContentType != "application/octet-stream" {
		t.Fatalf("video content type = %q, want application/octet-stream", video.ContentType)
	}
	if api.dlCalls["video"] != 1 {
		t.Fatalf("download calls = %#v, want exactly one video download", api.dlCalls)
	}

	// The video switch alone must not admit uploaded Office files, and the
	// adoc document has to keep taking the blocks path.
	if _, exists := byID["docx"]; exists {
		t.Fatalf("docx was ingested without include_uploaded_files: %#v", items)
	}
	if byID["adoc"].StoreWithoutIndexing {
		t.Fatal("a parsed adoc document was marked store-only")
	}
	if api.blockCalls["adoc"] != 1 {
		t.Fatalf("block calls = %#v, want the adoc document", api.blockCalls)
	}
}

// The default is off, so a video still costs nothing: no download, no item, and
// the skip is reported with the historical reason.
func TestFetchAllStillSkipsVideosByDefault(t *testing.T) {
	api := videoArchiveFixture()
	logs := captureLogs(t)
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfig("space"), []string{"space"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	for _, item := range items {
		if item.ExternalID == "video" || item.StoreWithoutIndexing {
			t.Fatalf("a video surfaced with the switch off: %#v", item)
		}
	}
	if len(api.dlCalls) != 0 {
		t.Fatalf("download calls = %#v, want none with the switch off", api.dlCalls)
	}
	want := "video files are deliberately not downloaded by this connector"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("skip log is missing %q:\n%s", want, logs.String())
	}
}

// parseDocumentSettings must read the new switch the same way it reads the old
// one: absent, malformed or unrelated keys leave it off.
func TestParseDocumentSettingsReadsTheUnindexedMediaSwitch(t *testing.T) {
	for _, testCase := range []struct {
		label    string
		settings map[string]interface{}
		want     bool
	}{
		{"unset", nil, false},
		{"empty bag", map[string]interface{}{}, false},
		{"explicit true", map[string]interface{}{"store_unindexed_media": true}, true},
		{"explicit false", map[string]interface{}{"store_unindexed_media": false}, false},
		{"string true", map[string]interface{}{"store_unindexed_media": "true"}, true},
		{"string ON", map[string]interface{}{"store_unindexed_media": " ON "}, true},
		{"string 1", map[string]interface{}{"store_unindexed_media": "1"}, true},
		{"string off", map[string]interface{}{"store_unindexed_media": "off"}, false},
		{"garbage string", map[string]interface{}{"store_unindexed_media": "maybe"}, false},
		{"wrong type", map[string]interface{}{"store_unindexed_media": 1}, false},
		{"unrelated key", map[string]interface{}{"include_media": true}, false},
		// The two switches are independent: turning one on never turns the
		// other on.
		{"the other switch on", map[string]interface{}{"include_uploaded_files": true}, false},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			cfg := testConfig()
			cfg.Settings = testCase.settings
			if got := parseDocumentSettings(cfg).StoreUnindexedMedia; got != testCase.want {
				t.Fatalf("StoreUnindexedMedia = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The picker offers the videos the sync will actually keep, and nothing else.
func TestListResourcesOffersVideosOnlyWithTheSwitchOn(t *testing.T) {
	for _, testCase := range switchStates(t, "store_unindexed_media") {
		t.Run(testCase.label, func(t *testing.T) {
			api := videoArchiveFixture()
			rootID, err := encodeResourceReference(resourceReference{WorkspaceID: "space"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := testConfig()
			cfg.Settings = testCase.settings
			resources, err := testConnector(api).ListResources(context.Background(), cfg, rootID)
			if err != nil {
				t.Fatalf("ListResources() error = %v", err)
			}
			names := make(map[string]bool, len(resources))
			for _, resource := range resources {
				names[resource.Name] = true
			}
			if names["Lesson.mp4"] != testCase.on {
				t.Fatalf("Lesson.mp4 listed = %v, want %v (%#v)",
					names["Lesson.mp4"], testCase.on, names)
			}
			if names["Table.able"] {
				t.Fatalf("an unsupported node was listed: %#v", names)
			}
		})
	}
}

// A video selected directly as the sync scope is archived under the same rule,
// and stays an unresolvable scope while the switch is off.
func TestSelectingAVideoNeedsTheUnindexedMediaSwitch(t *testing.T) {
	resourceID, err := encodeResourceReference(resourceReference{WorkspaceID: "space", NodeID: "video"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("switch on", func(t *testing.T) {
		api := videoArchiveFixture()
		cfg := testConfig(resourceID)
		cfg.Settings = map[string]interface{}{"store_unindexed_media": true}
		items, err := testConnector(api).FetchAll(context.Background(), cfg, []string{resourceID})
		if err != nil {
			t.Fatalf("FetchAll() error = %v", err)
		}
		if len(items) != 1 || items[0].ExternalID != "video" || !items[0].StoreWithoutIndexing {
			t.Fatalf("FetchAll() = %#v, want the archived video", items)
		}
	})

	t.Run("switch off", func(t *testing.T) {
		api := videoArchiveFixture()
		cfg := testConfig(resourceID)
		items, err := testConnector(api).FetchAll(context.Background(), cfg, []string{resourceID})
		if err == nil {
			t.Fatalf("FetchAll() error = nil, want the unresolvable scope to fail")
		}
		if len(api.dlCalls) != 0 {
			t.Fatalf("download calls = %#v, want none while the switch is off", api.dlCalls)
		}
		for _, item := range items {
			if item.StoreWithoutIndexing {
				t.Fatalf("a video was archived with the switch off: %#v", item)
			}
		}
	})
}

// oversizedVideoFixture is videoArchiveFixture with the listing reporting the
// 596 MiB file a real tenant has. The bytes are never downloaded: the scan has
// to decide from metadata alone.
func oversizedVideoFixture() *fakeAPI {
	api := videoArchiveFixture()
	for _, nodes := range api.nodes {
		for i := range nodes {
			if nodes[i].ID == "video" {
				nodes[i].FileSize = 596 << 20
			}
		}
	}
	return api
}

// A video the listing already reports as larger than maxDocumentBytes is a
// skip, not a failed download: the reason names the actual size and the cap so
// the operator can shrink, split or upload it elsewhere instead of re-running a
// sync that can never succeed.
func TestOversizedArchivedVideoIsSkippedWithItsSize(t *testing.T) {
	settings := documentSettings{StoreUnindexedMedia: true}
	oversized := node{Type: "FILE", Category: "VIDEO", Extension: "mp4", FileSize: 596 << 20}
	want := fmt.Sprintf("video is %d MiB, over the %d MiB single-file transfer limit",
		596, maxDocumentBytes>>20)
	if got := skipReason(oversized, settings); got != want {
		t.Fatalf("skipReason() = %q, want %q", got, want)
	}
	if !strings.Contains(want, "596") || !strings.Contains(want, "64") {
		t.Fatalf("reason %q does not name both the actual size and the cap", want)
	}
	if oversized.isIngestible(settings) {
		t.Fatal("an oversized archived video was reported as ingestible")
	}

	// A file that fits is untouched, both well below the cap and exactly at
	// it: the transfer limit is a strict upper bound, not a conservative one.
	for _, size := range []int64{14 << 20, maxDocumentBytes} {
		fits := node{Type: "FILE", Category: "VIDEO", Extension: "mp4", FileSize: size}
		if fits.isOversized() {
			t.Fatalf("fileSize %d was treated as oversized", size)
		}
		if got := skipReason(fits, settings); got != "" {
			t.Fatalf("skipReason(fileSize=%d) = %q, want no skip", size, got)
		}
	}
}

// The listing does not always carry fileSize; zero is "unknown", not "empty".
// Such a node must still be attempted, with the transfer cap as the backstop:
// absent metadata must never silently drop a video that might fit.
func TestUnknownFileSizeIsNotTreatedAsOversized(t *testing.T) {
	settings := documentSettings{StoreUnindexedMedia: true}
	for _, video := range []node{
		{Type: "FILE", Category: "VIDEO", Extension: "mp4"},
		{Type: "FILE", Category: "VIDEO", Extension: "mp4", FileSize: 0},
	} {
		if video.isOversized() {
			t.Fatalf("node %#v was treated as oversized without a fileSize", video)
		}
		if got := skipReason(video, settings); got != "" {
			t.Fatalf("skipReason(%#v) = %q, want no skip", video, got)
		}
		if !video.isIngestible(settings) {
			t.Fatalf("node %#v is not ingestible, want a download attempt", video)
		}
	}
}

// Only an archived video is subject to the metadata pre-check: an uploaded
// Office file or an audio file above the cap keeps its normal path and is
// bounded by the transfer cap instead, so the new skip can never widen into a
// format the video switch does not govern.
func TestOversizedSkipIsScopedToArchivedVideos(t *testing.T) {
	const oversizedFileSize = 596 << 20
	enabled := documentSettings{IncludeUploadedFiles: true}
	for _, candidate := range []node{
		{Type: "FILE", Category: "DOCUMENT", Extension: "docx", FileSize: oversizedFileSize},
		{Type: "FILE", Category: "AUDIO", Extension: "mp3", FileSize: oversizedFileSize},
	} {
		if got := skipReason(candidate, enabled); got != "" {
			t.Fatalf("skipReason(%s) = %q, want the normal ingest path", candidate.Extension, got)
		}
		if !candidate.isIngestible(enabled) {
			t.Fatalf("%s was refused because of its size", candidate.Extension)
		}
	}

	// With the video switch off the reason is still the plain video skip, not
	// a size complaint: the switch, not the size, decides that path.
	off := node{Type: "FILE", Category: "VIDEO", Extension: "mp4", FileSize: oversizedFileSize}
	const mediaReason = "video files are deliberately not downloaded by this connector"
	if got := skipReason(off, documentSettings{}); got != mediaReason {
		t.Fatalf("skipReason(switch off) = %q, want %q", got, mediaReason)
	}
}

// End to end: an oversized archived video is reported as skipped at scan time,
// never downloaded, and never surfaces as a failed sync item.
func TestFetchAllSkipsOversizedArchivedVideos(t *testing.T) {
	api := oversizedVideoFixture()
	logs := captureLogs(t)
	items, err := testConnector(api).FetchAll(
		context.Background(), testConfigWithStoredMedia("space"), []string{"space"},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	for _, item := range items {
		if item.ExternalID == "video" {
			t.Fatalf("an oversized video was downloaded: %#v", item)
		}
	}
	if api.dlCalls["video"] != 0 {
		t.Fatalf("download calls = %#v, want none for the oversized video", api.dlCalls)
	}
	want := fmt.Sprintf("video is %d MiB, over the %d MiB single-file transfer limit",
		596, maxDocumentBytes>>20)
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("skip log is missing %q:\n%s", want, logs.String())
	}
}

// The listing's size field is named "size"; a struct tag typos would decode
// silently as zero, which makes the oversized skip unreachable without failing
// any hand-built node value. Decode a real response shape instead.
func TestNodeFileSizeIsDecodedFromTheListingSizeField(t *testing.T) {
	const response = `{"nodes": [
		{"nodeId": "n1", "name": "Lesson.mp4", "type": "FILE", "category": "VIDEO",
		 "extension": "mp4", "size": 624716345},
		{"nodeId": "n2", "name": "Handout.pdf", "type": "FILE", "category": "DOCUMENT",
		 "extension": "pdf"}
	]}`

	var decoded struct {
		Nodes []node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatalf("decode a real listing response: %v", err)
	}
	if len(decoded.Nodes) != 2 {
		t.Fatalf("decoded nodes = %d, want 2", len(decoded.Nodes))
	}
	if got, want := decoded.Nodes[0].FileSize, int64(624716345); got != want {
		t.Fatalf("video FileSize = %d, want %d (the listing calls this field \"size\")", got, want)
	}
	if !decoded.Nodes[0].isOversized() {
		t.Fatalf("a 596 MiB video should be oversized")
	}
	if got := decoded.Nodes[1].FileSize; got != 0 {
		t.Fatalf("absent size should decode as unknown (0), got %d", got)
	}
	if decoded.Nodes[1].isOversized() {
		t.Fatalf("unknown size must never be treated as oversized")
	}
}
