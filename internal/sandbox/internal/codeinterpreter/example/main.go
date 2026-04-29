// Example command demonstrating typical use of the Go Code Interpreter SDK.
//
// Usage:
//
//	E2B_API_KEY=e2b_... go run ./go/example
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox/internal/codeinterpreter"
)

var (
	code = `
import numpy
from PIL import Image

imarray = numpy.random.rand(16,16,3) * 255
image = Image.fromarray(imarray.astype('uint8')).convert('RGBA')

image.save("test.png")
print("Image saved.")`
)

func main() {
	ctx := context.Background()

	sbx, err := codeinterpreter.Create(ctx, &codeinterpreter.SandboxOpts{
		Timeout: 60 * time.Second,
	})
	if err != nil {
		log.Fatalf("create sandbox: %v", err)
	}

	fmt.Println("ℹ️  sandbox created", sbx.SandboxID())
	defer func() {
		_ = sbx.Kill(ctx)
		fmt.Println("🧹 sandbox killed")
	}()

	exec, err := sbx.RunCode(ctx, code, &codeinterpreter.RunCodeOpts{
		OnStdout: func(msg codeinterpreter.OutputMessage) {
			fmt.Printf("[stdout] %s", msg.Line)
		},
		OnStderr: func(msg codeinterpreter.OutputMessage) {
			fmt.Printf("[stderr] %s", msg.Line)
		},
	})
	if err != nil {
		log.Fatalf("run code: %v", err)
	}

	fmt.Println("result:", exec.Results[0].PNG)
	if exec.Error != nil {
		fmt.Println("error:", exec.Error)
	}
}
