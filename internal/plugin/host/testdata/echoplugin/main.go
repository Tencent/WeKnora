// Command echoplugin is a host plugin for the host package's tests. Its
// behaviour is driven by the query it is asked and by a "version" file next
// to it, so a test can install a package that lies about its version.
package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

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
		// sleep:<duration> answers after a while, to keep a call in flight.
		if d, ok := strings.CutPrefix(in.Query, "sleep:"); ok {
			wait, err := time.ParseDuration(d)
			if err != nil {
				return nil, err
			}
			time.Sleep(wait)
			return result("slept"), nil
		}
		// dial:<addr> connects without the proxy; fetch:<url> GETs through
		// the environment's proxy settings.
		if addr, ok := strings.CutPrefix(in.Query, "dial:"); ok {
			c, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				return result("dial failed"), nil
			}
			_ = c.Close()
			return result("dial ok"), nil
		}
		if url, ok := strings.CutPrefix(in.Query, "fetch:"); ok {
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
			resp, err := client.Get(url)
			if err != nil {
				return result("fetch failed: " + err.Error()), nil
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return result(strconv.Itoa(resp.StatusCode) + " " + strings.TrimSpace(string(body))), nil
		}
		return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{Title: in.Query, URL: "echo"}}}, nil
	}))
	if err := p.Serve(); err != nil {
		log.Fatal(err)
	}
}

func result(title string) *pluginapi.SearchOutput {
	return &pluginapi.SearchOutput{Results: []pluginapi.SearchResult{{Title: title, URL: "probe"}}}
}
