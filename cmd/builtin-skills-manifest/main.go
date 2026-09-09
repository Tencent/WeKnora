// Emit the release manifest for Docker labels and cloud template publication.
package main

import (
	"encoding/json"
	"os"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(builtin.PublishedManifest()); err != nil {
		panic(err)
	}
}
