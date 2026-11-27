package storageurl

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamRewriter_SplitStorageScheme(t *testing.T) {
	for _, scheme := range []string{"resource", "storage", "local", "minio", "s3", "cos", "tos", "oss", "obs", "ks3"} {
		ref := scheme + "://bucket/10000/image.png"
		if scheme == "resource" {
			ref = "resource://xifDo7NTSL300Lp1goVutw"
		}
		if scheme == "storage" {
			ref = "storage://backend-a/minio://bucket/10000/image.png"
		}
		input := "See [file](" + ref + ") for details."
		for split := 1; split < len(scheme+"://"); split++ {
			t.Run(fmt.Sprintf("%s/%d", scheme, split), func(t *testing.T) {
				sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
				at := len("See [file](") + split
				got := sr.Push(context.Background(), "answer", input[:at], false, nil)
				got += sr.Push(context.Background(), "answer", input[at:], true, nil)
				require.Equal(t, "See [file](https://cdn.example.com/x.png) for details.", got)
				require.Empty(t, sr.FlushAll(context.Background()))
			})
		}
	}
}

func TestStreamRewriter_SchemePrefixProse(t *testing.T) {
	ctx := context.Background()
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	require.Equal(t, "A ", sr.Push(ctx, "answer", "A res", false, nil))
	require.Equal(t, "resource is useful", sr.Push(ctx, "answer", "ource is useful", true, nil))
	require.Equal(t, "plain words tacos", sr.Push(ctx, "answer", "plain words tacos", false, nil))
	require.Equal(t, "ends_with_s", sr.Push(ctx, "answer", "ends_with_s", false, nil))
	require.Equal(t, "", sr.Push(ctx, "answer", "minio:/", false, nil))
	require.Equal(t, "minio:/", sr.Push(ctx, "answer", "", true, nil))
}

func TestStreamRewriter_BytewiseStorageReference(t *testing.T) {
	ctx := context.Background()
	sr := NewStreamRewriter(NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST"))
	input := "[file](storage://backend-a/minio://bucket/10000/image.png)"
	got := ""
	for i := range len(input) {
		got += sr.Push(ctx, "answer", input[i:i+1], false, nil)
	}
	got += sr.Push(ctx, "answer", "", true, nil)
	require.Equal(t, "[file](https://cdn.example.com/x.png)", got)
}
