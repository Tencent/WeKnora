// Command weknora-plugin-conformance checks a running plugin against the
// extension protocol.
//
//	weknora-plugin-conformance -url https://plugin.example.com -secret $SECRET
//	weknora-plugin-conformance -socket /tmp/p.sock -token $TOKEN
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func main() {
	url := flag.String("url", "", "base URL of a remote plugin")
	socket := flag.String("socket", "", "unix socket of a plugin started by hand in host mode")
	token := flag.String("token", "", "host token (with -socket)")
	secret := flag.String("secret", "", "shared secret (with -url)")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	timeout := flag.Duration("timeout", 30*time.Second, "per-check timeout")
	flag.Parse()

	var c, anon *client.Client
	var raw func(ctx context.Context, path string, body []byte) (*http.Response, error)
	switch {
	case *socket != "":
		hs := pluginapi.Handshake{Protocol: pluginapi.ProtocolVersion, Network: "unix", Address: *socket}
		c = client.ForHandshake(hs, client.Bearer(*token))
		anon = client.ForHandshake(hs, nil)
	case *url != "":
		var auth client.Auth
		if *secret != "" {
			auth = client.Signed(*secret)
			anon = client.New(*url, nil, nil)
		}
		c = client.New(*url, nil, auth)
		raw = func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, *url+path, bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			if auth != nil {
				_ = auth.Apply(req, body)
			}
			return http.DefaultClient.Do(req)
		}
	default:
		fmt.Fprintln(os.Stderr, "give -url or -socket")
		os.Exit(2)
	}

	rep := conformance.Run(context.Background(), conformance.Target{
		Client: c, Unauthenticated: anon, Raw: raw, Timeout: *timeout,
	})
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(rep)
	} else {
		fmt.Printf("plugin %s\n", rep.Plugin)
		for _, r := range rep.Results {
			mark := "PASS"
			if !r.Passed {
				mark = "FAIL"
			}
			fmt.Printf("  %s  %s", mark, r.Name)
			if r.Detail != "" {
				fmt.Printf(" — %s", r.Detail)
			}
			fmt.Println()
		}
	}
	if !rep.Passed() {
		os.Exit(1)
	}
}
