package artifact

import (
	"context"
	"testing"
)

func benchmarkExpected(b *testing.B) Expected {
	b.Helper()
	key, err := BuildKey(1, testKeyMaterial())
	if err != nil {
		b.Fatal(err)
	}
	return Expected{
		Key:   key,
		Codec: CodecJSONV1,
		Validate: func(payload []byte) error {
			return nil
		},
	}
}

func BenchmarkRuntimeColdCompute(b *testing.B) {
	ctx := context.Background()
	expected := benchmarkExpected(b)
	payload := []byte(`{"value":"computed"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runtime := NewRuntime(newMemoryArtifactRepository(), nil)
		if _, err := runtime.LoadOrCompute(
			ctx,
			expected,
			func(context.Context) ([]byte, error) { return payload, nil },
		); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(1, "provider_calls/op")
}

func BenchmarkRuntimeWarmHit(b *testing.B) {
	ctx := context.Background()
	expected := benchmarkExpected(b)
	payload := []byte(`{"value":"computed"}`)
	runtime := NewRuntime(newMemoryArtifactRepository(), nil)
	if _, err := runtime.LoadOrCompute(
		ctx,
		expected,
		func(context.Context) ([]byte, error) { return payload, nil },
	); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := runtime.LoadOrCompute(
			ctx,
			expected,
			func(context.Context) ([]byte, error) {
				b.Fatal("warm artifact unexpectedly called the provider")
				return nil, nil
			},
		); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(0, "provider_calls/op")
}
