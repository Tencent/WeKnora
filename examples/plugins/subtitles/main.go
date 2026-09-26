// Command subtitles is an example parser plugin: it turns SRT and WebVTT
// subtitle files into a Markdown transcript, one paragraph per cue with its
// start time. It also keeps a per-workspace count of parsed files through
// the Host API key-value store, to show how a plugin keeps state without a
// database.
//
// Build a package with ./package.sh.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Version must match plugin.yaml.
const Version = "1.0.0"

func main() {
	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.subtitles", Version: Version})
	p.Parser("subtitles", pluginsdk.ParserFunc(parse))
	if err := p.Serve(); err != nil {
		log.Fatal(err)
	}
}

// timing matches a cue timing line: 00:01:02,500 --> 00:01:04,000 (SRT) or
// 01:02.500 --> 01:04.000 (WebVTT, hours optional).
var timing = regexp.MustCompile(`^\s*((?:\d+:)?\d{2}:\d{2}[.,]\d{3})\s*-->\s*`)

// tags strips WebVTT / SRT inline markup such as <i> or <v Speaker>.
var tags = regexp.MustCompile(`<[^>]*>`)

type cue struct {
	start string
	text  []string
}

func parse(ctx context.Context, call *pluginsdk.Call, in pluginapi.ParseInput) (*pluginapi.ParseOutput, error) {
	if len(in.Content) == 0 {
		return nil, pluginapi.Errorf(pluginapi.CodeInvalidConfig, "the subtitle file is empty")
	}
	cues := parseCues(in.Content)
	if len(cues) == 0 {
		return nil, pluginapi.Errorf(
			pluginapi.CodeInvalidConfig,
			"no subtitle cues found; is this an .srt or .vtt file?",
		)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSuffix(in.FileName, "."+in.FileType)
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", title)
	for _, c := range cues {
		fmt.Fprintf(&md, "**[%s]** %s\n\n", shortTime(c.start), strings.Join(c.text, " "))
	}
	countParsed(ctx, call)
	return &pluginapi.ParseOutput{
		Markdown: strings.TrimSpace(md.String()) + "\n",
		Metadata: map[string]string{"cues": fmt.Sprint(len(cues))},
	}, nil
}

func parseCues(data []byte) []cue {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var out []cue
	var cur *cue
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := timing.FindStringSubmatch(line); m != nil {
			out = append(out, cue{start: m[1]})
			cur = &out[len(out)-1]
			continue
		}
		if strings.TrimSpace(line) == "" {
			cur = nil
			continue
		}
		if cur == nil {
			continue // cue numbers, WEBVTT header, NOTE blocks
		}
		if text := strings.TrimSpace(tags.ReplaceAllString(line, "")); text != "" {
			cur.text = append(cur.text, text)
		}
	}
	kept := out[:0]
	for _, c := range out {
		if len(c.text) > 0 {
			kept = append(kept, c)
		}
	}
	return kept
}

// shortTime drops milliseconds and a zero hour: 00:01:02,500 → 01:02.
func shortTime(t string) string {
	t = strings.NewReplacer(",", ".").Replace(t)
	if i := strings.IndexByte(t, '.'); i >= 0 {
		t = t[:i]
	}
	return strings.TrimPrefix(t, "00:")
}

// countParsed bumps the workspace's parsed-file counter. It is best effort:
// a missing grant or an unreachable host must not fail the parse.
func countParsed(ctx context.Context, call *pluginsdk.Call) {
	host := call.Host()
	if host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var n int
	if _, err := host.KVGet(ctx, "stats/parsed", &n); err != nil {
		return
	}
	_ = host.KVPut(ctx, "stats/parsed", n+1, 0)
}
