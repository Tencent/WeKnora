// Command echoplugin is a host plugin for the host package's tests. Its
// behaviour is driven by the query it is asked and by a "version" file next
// to it, so a test can install a package that lies about its version.
package main

import (
	"context"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func main() {
	version := os.Getenv(pluginapi.EnvPluginVersion)
	if b, err := os.ReadFile("version"); err == nil {
		version = strings.TrimSpace(string(b))
	}
	p := pluginsdk.New(pluginsdk.Info{ID: os.Getenv(pluginapi.EnvPluginID), Version: version})
	p.WebSearch("echo", pluginsdk.WebSearchFunc(func(_ context.Context, _ *pluginsdk.Call, in pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
		switch in.Query {
		case "crash":
			os.Exit(3)
		case "env":
			var keys []string
			for _, kv := range os.Environ() {
				k, _, _ := strings.Cut(kv, "=")
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{Title: strings.Join(keys, ","), URL: "env"}}}, nil
		case "pid":
			return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{Title: strconv.Itoa(os.Getpid()), URL: "pid"}}}, nil
		}
		return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{Title: in.Query, URL: "echo"}}}, nil
	}))
	if err := p.Serve(); err != nil {
		log.Fatal(err)
	}
}
