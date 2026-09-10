// Emit the release manifest for Docker labels and cloud template publication.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
)

func main() {
	profile := flag.String("profile", "office-core", "image profile: office-core")
	version := flag.Bool("version", false, "print the builtin bundle version")
	flag.Parse()
	if *version {
		fmt.Println(builtin.Version)
		return
	}
	manifest, err := builtin.PublishedManifestForProfile(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
		panic(err)
	}
}
