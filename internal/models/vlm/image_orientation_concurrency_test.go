package vlm

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orientationDecodeProbe replaces the decoder seam for one test. It counts
// every call; when hold is set the FIRST call stops inside the decoder and
// reports it on entered, so a test can keep the (single) conversion slot busy
// for exactly as long as it needs instead of racing a timer. Later calls go
// straight to the real decoder, so a test whose gate is missing fails on an
// assertion instead of deadlocking on a conversion nobody releases.
type orientationDecodeProbe struct {
	calls   atomic.Int64
	hold    bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func installOrientationDecode(t *testing.T, probe *orientationDecodeProbe) *orientationDecodeProbe {
	t.Helper()
	previous := decodeImage
	decodeImage = func(r io.Reader) (image.Image, string, error) {
		if probe.calls.Add(1) == 1 && probe.hold {
			probe.entered <- struct{}{}
			<-probe.release
		}
		return previous(r)
	}
	t.Cleanup(func() {
		probe.releaseAll()
		decodeImage = previous
	})
	return probe
}

// holdOrientationDecode stops the first conversion inside the decoder, which is
// how a test occupies the only conversion slot deterministically.
func holdOrientationDecode(t *testing.T) *orientationDecodeProbe {
	t.Helper()
	return installOrientationDecode(t, &orientationDecodeProbe{
		hold:    true,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	})
}

// countOrientationDecodes only counts, so a conversion that unexpectedly
// reaches the decoder fails an assertion instead of blocking.
func countOrientationDecodes(t *testing.T) *orientationDecodeProbe {
	t.Helper()
	return installOrientationDecode(t, &orientationDecodeProbe{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	})
}

// waitInsideDecoder blocks until the held conversion has reached the decoder.
// The timeout guards a broken fixture; it never decides the outcome of the test.
func (p *orientationDecodeProbe) waitInsideDecoder(t *testing.T) {
	t.Helper()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the conversion never reached the decoder")
	}
}

// releaseAll lets a held conversion finish; safe to call more than once.
func (p *orientationDecodeProbe) releaseAll() { p.once.Do(func() { close(p.release) }) }

// runOrientationConversion starts one Predict in the background and returns a
// join func. The cleanup is a safety net: a conversion left inside the decoder
// by a failing assertion is always released and joined before the seam is
// restored, so it cannot leak its slot into the next test.
func runOrientationConversion(
	ctx context.Context, t *testing.T, model VLM, data []byte, probe *orientationDecodeProbe,
) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = model.Predict(ctx, [][]byte{data}, "prompt")
	}()
	t.Cleanup(func() {
		probe.releaseAll()
		<-done
	})
	return func() {
		probe.releaseAll()
		<-done
	}
}

// fillOrientationSlot takes the whole budget from the test itself, failing fast
// instead of blocking when an earlier test leaked a slot.
func fillOrientationSlot(t *testing.T) {
	t.Helper()
	select {
	case orientationSlots <- struct{}{}:
	default:
		t.Fatal("a conversion slot was already held before this test started")
	}
	t.Cleanup(func() { <-orientationSlots })
}

func requireOrientationSlotFree(t *testing.T) {
	t.Helper()
	require.Zero(t, len(orientationSlots), "the conversion slot must be released")
}

// A slot belongs to the process, not to the instance that took it: two VLM
// instances built separately (as every model and every configuration change
// does) must share one budget. While the first sits in the decoder, the second
// must forward its bytes instead of starting a conversion of its own.
func TestOrientationSlotIsSharedByEveryVLMInstance(t *testing.T) {
	// Pin the switch on: these cases assert what the pass does, so they must
	// not depend on whatever the ambient environment exports.
	t.Setenv("VLM_IMAGE_ORIENTATION", "on")
	stored := markedJPEG(t, 6, binary.BigEndian)
	expected := append([]byte(nil), stored...)

	firstInner, secondInner := &recordingVLM{}, &recordingVLM{}
	first, err := wrapVLMImageOrientation(firstInner, nil)
	require.NoError(t, err)
	second, err := wrapVLMImageOrientation(secondInner, nil)
	require.NoError(t, err)

	probe := holdOrientationDecode(t)
	wait := runOrientationConversion(context.Background(), t, first, stored, probe)
	probe.waitInsideDecoder(t)

	_, err = second.Predict(context.Background(), [][]byte{stored}, "prompt")
	require.NoError(t, err)

	require.Len(t, secondInner.received, 1)
	assert.Equal(t, expected, secondInner.received[0], "a full budget must forward the bytes unchanged")
	assert.Same(t, &stored[0], &secondInner.received[0][0], "the payload must be forwarded by reference")
	assert.Equal(t, int64(1), probe.calls.Load(), "the refused call must not reach the decoder")

	wait()
	width, height := decodeSize(t, firstInner.received[0])
	assert.Equal(t, 8, width, "the instance that owns the slot still gets upright pixels")
	assert.Equal(t, 24, height)
}

// The budget is not the background governor's business: an interactive chat
// upload and a background enrichment draw from the same slot, in both
// directions, whichever one of them got there first.
func TestOrientationSlotIsSharedByInteractiveAndBackgroundCalls(t *testing.T) {
	// Pin the switch on: these cases assert what the pass does, so they must
	// not depend on whatever the ambient environment exports.
	t.Setenv("VLM_IMAGE_ORIENTATION", "on")
	for _, test := range []struct {
		name      string
		holderCtx context.Context
		callerCtx context.Context
	}{
		{
			name:      "background conversion refuses an interactive call",
			holderCtx: types.WithBackgroundTask(context.Background()),
			callerCtx: context.Background(),
		},
		{
			name:      "interactive conversion refuses a background call",
			holderCtx: context.Background(),
			callerCtx: types.WithBackgroundTask(context.Background()),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored := markedJPEG(t, 6, binary.BigEndian)
			expected := append([]byte(nil), stored...)

			holderInner, callerInner := &recordingVLM{}, &recordingVLM{}
			holder, err := wrapVLMImageOrientation(holderInner, nil)
			require.NoError(t, err)
			caller, err := wrapVLMImageOrientation(callerInner, nil)
			require.NoError(t, err)

			probe := holdOrientationDecode(t)
			wait := runOrientationConversion(test.holderCtx, t, holder, stored, probe)
			probe.waitInsideDecoder(t)

			_, err = caller.Predict(test.callerCtx, [][]byte{stored}, "prompt")
			require.NoError(t, err)

			require.Len(t, callerInner.received, 1)
			assert.Equal(t, expected, callerInner.received[0], "the caller must get its bytes back unchanged")
			assert.Same(t, &stored[0], &callerInner.received[0][0])
			assert.Equal(t, int64(1), probe.calls.Load(), "only the holder may decode")

			wait()
		})
	}
}

// With the budget full the payload reaches the model byte for byte, tag
// included, and the decoder is never entered — the same shape as the
// over-budget pixel branch.
func TestOrientationSlotExhaustionForwardsThePayloadUntouched(t *testing.T) {
	// Pin the switch on: these cases assert what the pass does, so they must
	// not depend on whatever the ambient environment exports.
	t.Setenv("VLM_IMAGE_ORIENTATION", "on")
	stored := markedJPEG(t, 6, binary.BigEndian)
	expected := append([]byte(nil), stored...)

	fillOrientationSlot(t) // someone else in the process owns the only slot
	probe := countOrientationDecodes(t)

	out := orientImageBytes(context.Background(), stored)

	assert.Equal(t, expected, out, "the bytes must come back unchanged")
	assert.Same(t, &stored[0], &out[0], "the payload must come back by reference")
	assert.Equal(t, 6, jpegEXIFOrientation(out), "the rotation tag must survive the passthrough")
	assert.Zero(t, probe.calls.Load(), "an exhausted budget must not decode anything")
}

// Every return path must give the slot back. A leak would not fail loudly: it
// would silently turn normalisation off for every later call in the process, so
// each case proves that the next conversion still gets its slot.
func TestOrientationSlotIsReleasedOnEveryReturnPath(t *testing.T) {
	// Pin the switch on: these cases assert what the pass does, so they must
	// not depend on whatever the ambient environment exports.
	t.Setenv("VLM_IMAGE_ORIENTATION", "on")
	stored := markedJPEG(t, 6, binary.BigEndian)

	t.Run("conversion succeeds", func(t *testing.T) {
		probe := countOrientationDecodes(t)

		oriented := orientImageBytes(context.Background(), stored)
		width, height := decodeSize(t, oriented)
		require.Equal(t, []int{8, 24}, []int{width, height})
		require.Equal(t, int64(1), probe.calls.Load())
		requireOrientationSlotFree(t)

		again := orientImageBytes(context.Background(), stored)
		againWidth, againHeight := decodeSize(t, again)
		assert.Equal(t, []int{8, 24}, []int{againWidth, againHeight},
			"a finished conversion must leave its slot available")
	})

	t.Run("decode fails", func(t *testing.T) {
		// A real payload cut just past its scan header: the header probe is
		// happy, the full decode is not.
		broken := truncatedAfterHeader(t, stored)
		require.Equal(t, 6, jpegEXIFOrientation(broken))
		_, _, configErr := image.DecodeConfig(bytes.NewReader(broken))
		require.NoError(t, configErr, "the fixture must get past the pixel-budget probe")
		_, _, decodeErr := image.Decode(bytes.NewReader(broken))
		require.Error(t, decodeErr, "the fixture must fail the full decode")

		out := orientImageBytes(context.Background(), broken)
		assert.Same(t, &broken[0], &out[0], "a failed decode must forward the stored bytes")
		requireOrientationSlotFree(t)

		next := orientImageBytes(context.Background(), stored)
		nextWidth, nextHeight := decodeSize(t, next)
		assert.Equal(t, []int{8, 24}, []int{nextWidth, nextHeight},
			"a failed conversion must leave its slot available")
	})
}

// truncatedAfterHeader cuts a JPEG just past its start-of-scan marker: enough
// for image.DecodeConfig, not enough for image.Decode.
func truncatedAfterHeader(t *testing.T, data []byte) []byte {
	t.Helper()
	for i := 2; i+1 < len(data); i++ {
		if data[i] == 0xFF && data[i+1] == jpegMarkerSOS {
			return data[:i+4]
		}
	}
	t.Fatal("fixture has no start-of-scan marker")
	return nil
}
