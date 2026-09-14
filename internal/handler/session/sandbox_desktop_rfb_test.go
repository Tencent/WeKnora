package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRFBValidateServerHandshake(t *testing.T) {
	t.Run("accepts 3.8 with None only", func(t *testing.T) {
		require.NoError(t, rfbValidateServerHandshake([]byte("RFB 003.008\n"), []byte{1}))
	})
	t.Run("rejects 3.3", func(t *testing.T) {
		// 3.3 puts 13 bytes on the client side, not 14. Miscounting by one
		// loses framing permanently.
		err := rfbValidateServerHandshake([]byte("RFB 003.003\n"), []byte{1})
		require.Error(t, err)
	})
	t.Run("rejects extra security types", func(t *testing.T) {
		// x11vnc runs with -nopw, so None must be the only offer. Anything
		// else means the start script did not run as expected, and relaying
		// on would hand the authentication semantics to an unknown path.
		err := rfbValidateServerHandshake([]byte("RFB 003.008\n"), []byte{1, 2})
		require.Error(t, err)
	})
	t.Run("rejects VNC-Auth only", func(t *testing.T) {
		require.Error(t, rfbValidateServerHandshake([]byte("RFB 003.008\n"), []byte{2}))
	})
	t.Run("rejects empty security list", func(t *testing.T) {
		require.Error(t, rfbValidateServerHandshake([]byte("RFB 003.008\n"), nil))
	})
}

func TestRFBReadServerVersionStopsAtTwelveBytes(t *testing.T) {
	// RFB 3.8 §7.1.1: the server sends its 12-byte ProtocolVersion and then
	// waits for the CLIENT's version before offering security types. The
	// relay never answers — noVNC does — so a reader that waits for 13 bytes
	// hangs until its deadline. Verified against the live sandbox: silent
	// client, zero further bytes in 10s; after replying, types arrive in
	// 0.25s.
	reads := 0
	prelude, err := rfbReadServerVersion(func() ([]byte, error) {
		reads++
		if reads > 1 {
			return nil, errRFBHandshakeIncomplete
		}
		return []byte("RFB 003.008\n"), nil
	})
	require.NoError(t, err)
	require.Equal(t, []byte("RFB 003.008\n"), prelude)
	require.Equal(t, 1, reads, "a second read would block until the deadline")
}

func TestRFBReadServerVersionRejectsWrongVersion(t *testing.T) {
	_, err := rfbReadServerVersion(func() ([]byte, error) {
		return []byte("RFB 003.003\n"), nil
	})
	require.Error(t, err)
}

func TestRFBReadServerVersionKeepsCoalescedBytes(t *testing.T) {
	// Nothing stops a server from coalescing; whatever arrived must reach
	// the browser.
	one := append([]byte("RFB 003.008\n"), 1, 1)
	prelude, err := rfbReadServerVersion(func() ([]byte, error) { return one, nil })
	require.NoError(t, err)
	require.Equal(t, one, prelude)
}

func TestRFBServerStreamValidatesSecurityTypes(t *testing.T) {
	// The "exactly [None]" assertion cannot run before the upgrade any more,
	// so the relay checks it on the first server message instead.
	t.Run("accepts None only", func(t *testing.T) {
		var s rfbServerStream
		require.NoError(t, s.Observe([]byte{1, 1}))
		// Later traffic is no longer inspected.
		require.NoError(t, s.Observe([]byte{0, 0, 0, 0}))
	})
	t.Run("accepts a split list", func(t *testing.T) {
		var s rfbServerStream
		require.NoError(t, s.Observe([]byte{1}))
		require.NoError(t, s.Observe([]byte{1}))
	})
	t.Run("rejects VNC-Auth", func(t *testing.T) {
		var s rfbServerStream
		require.Error(t, s.Observe([]byte{1, 2}))
	})
	t.Run("rejects extra types", func(t *testing.T) {
		var s rfbServerStream
		require.Error(t, s.Observe([]byte{2, 1, 2}))
	})
	t.Run("rejects a zero-length offer", func(t *testing.T) {
		// count 0 is the RFB failure form, not an offer.
		var s rfbServerStream
		require.Error(t, s.Observe([]byte{0}))
	})
}

func TestRFBReadServerHandshake(t *testing.T) {
	// The two assertions must run BEFORE the browser socket is upgraded, so
	// a failure is still an HTTP status code rather than a close frame. That
	// means reading the server's opening bytes off the sandbox connection and
	// replaying them afterwards, which is what prelude is for.
	t.Run("returns the prelude for replay", func(t *testing.T) {
		frames := [][]byte{[]byte("RFB 003.008\n"), {1, 1}}
		i := 0
		prelude, err := rfbReadServerHandshake(func() ([]byte, error) {
			if i >= len(frames) {
				return nil, errRFBHandshakeIncomplete
			}
			f := frames[i]
			i++
			return f, nil
		})
		require.NoError(t, err)
		require.Equal(t, append([]byte("RFB 003.008\n"), 1, 1), prelude)
	})

	t.Run("keeps trailing bytes that belong to the message stream", func(t *testing.T) {
		// The server may coalesce the handshake with its first real message.
		// Those extra bytes must survive into the prelude, or the browser
		// never sees them.
		one := append([]byte("RFB 003.008\n"), 1, 1, 0xAA, 0xBB)
		prelude, err := rfbReadServerHandshake(func() ([]byte, error) { return one, nil })
		require.NoError(t, err)
		require.Equal(t, one, prelude)
	})

	t.Run("rejects RFB 3.3", func(t *testing.T) {
		_, err := rfbReadServerHandshake(func() ([]byte, error) {
			return append([]byte("RFB 003.003\n"), 1, 1), nil
		})
		require.Error(t, err)
	})

	t.Run("rejects a non-None security type", func(t *testing.T) {
		_, err := rfbReadServerHandshake(func() ([]byte, error) {
			return append([]byte("RFB 003.008\n"), 1, 2), nil
		})
		require.Error(t, err)
	})

	t.Run("propagates a read failure", func(t *testing.T) {
		_, err := rfbReadServerHandshake(func() ([]byte, error) {
			return nil, errRFBHandshakeIncomplete
		})
		require.ErrorIs(t, err, errRFBHandshakeIncomplete)
	})
}

func TestRFBClientMessageSize(t *testing.T) {
	cases := []struct {
		name     string
		buf      []byte
		size     int
		complete bool
		known    bool
	}{
		{"SetPixelFormat", append([]byte{0}, make([]byte, 19)...), 20, true, true},
		{"SetPixelFormat short", []byte{0, 0, 0}, 20, false, true},
		{"SetEncodings 2 encodings", append([]byte{2, 0, 0, 2}, make([]byte, 8)...), 12, true, true},
		{"SetEncodings header short", []byte{2, 0}, 0, false, true},
		{"FramebufferUpdateRequest", append([]byte{3}, make([]byte, 9)...), 10, true, true},
		{"KeyEvent", append([]byte{4}, make([]byte, 7)...), 8, true, true},
		{"PointerEvent", append([]byte{5}, make([]byte, 5)...), 6, true, true},
		{"ClientCutText 5 bytes", append([]byte{6, 0, 0, 0, 0, 0, 0, 5}, make([]byte, 5)...), 13, true, true},
		// noVNC extendedClipboardNotify: length = toUnsigned32bit(-4).
		{
			"ClientCutText extended 4 bytes",
			append([]byte{6, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFC}, 1, 2, 3, 4),
			12, true, true,
		},
		{"EnableContinuousUpdates", append([]byte{150}, make([]byte, 9)...), 10, true, true},
		{"ClientFence 4 bytes", append([]byte{248, 0, 0, 0, 0, 0, 0, 0, 4}, make([]byte, 4)...), 13, true, true},
		{"SetDesktopSize 1 screen", append([]byte{251, 0, 0, 0, 0, 0, 1, 0}, make([]byte, 16)...), 24, true, true},
		{"unknown opcode", []byte{99, 1, 2, 3}, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			size, complete, known := rfbClientMessageSize(tc.buf)
			require.Equal(t, tc.known, known)
			require.Equal(t, tc.complete, complete)
			if tc.complete {
				require.Equal(t, tc.size, size)
			}
		})
	}
}

func TestRFBOpcodeCountsAsActivity(t *testing.T) {
	// Only real user input counts. FramebufferUpdateRequest (3) is driven by
	// the server's own updates, so counting it would make a wallpaper with a
	// clock look like a person at the keyboard — the whole reason byte
	// counting was rejected.
	require.True(t, rfbOpcodeCountsAsActivity(4))  // KeyEvent
	require.True(t, rfbOpcodeCountsAsActivity(5))  // PointerEvent
	require.True(t, rfbOpcodeCountsAsActivity(6))  // ClientCutText
	require.False(t, rfbOpcodeCountsAsActivity(0)) // SetPixelFormat
	require.False(t, rfbOpcodeCountsAsActivity(2)) // SetEncodings
	require.False(t, rfbOpcodeCountsAsActivity(3)) // FramebufferUpdateRequest
	require.False(t, rfbOpcodeCountsAsActivity(150))
	require.False(t, rfbOpcodeCountsAsActivity(248))
	require.False(t, rfbOpcodeCountsAsActivity(251))
}

func TestRFBClientStreamSkipsHandshakeThenDetectsActivity(t *testing.T) {
	s := &rfbClientStream{}

	// 14 handshake bytes, deliberately split across three chunks so the
	// counter cannot assume one frame per read.
	require.False(t, s.Consume([]byte("RFB 003.")))
	require.False(t, s.Consume([]byte("008\n")))
	require.False(t, s.Consume([]byte{1, 1}))
	require.True(t, s.ParsingEnabled())

	// FramebufferUpdateRequest: parsed, not activity.
	require.False(t, s.Consume(append([]byte{3}, make([]byte, 9)...)))
	// PointerEvent: activity.
	require.True(t, s.Consume(append([]byte{5}, make([]byte, 5)...)))
}

func TestRFBClientStreamHandlesSplitMessages(t *testing.T) {
	s := &rfbClientStream{}
	require.False(t, s.Consume(append([]byte("RFB 003.008\n"), 1, 1)))

	// One KeyEvent delivered one byte at a time.
	require.False(t, s.Consume([]byte{4}))
	for i := 0; i < 6; i++ {
		require.False(t, s.Consume([]byte{0}))
	}
	require.True(t, s.Consume([]byte{0}), "the final byte completes the KeyEvent")
}

func TestRFBClientStreamHandlesCoalescedMessages(t *testing.T) {
	s := &rfbClientStream{}
	require.False(t, s.Consume(append([]byte("RFB 003.008\n"), 1, 1)))

	// SetPixelFormat (no activity) followed by KeyEvent (activity) in one read.
	chunk := append(append([]byte{0}, make([]byte, 19)...), append([]byte{4}, make([]byte, 7)...)...)
	require.True(t, s.Consume(chunk))
}

func TestRFBClientStreamFailsClosedOnUnknownOpcode(t *testing.T) {
	// Guessing a length would desync the stream forever. The contract is:
	// stop parsing, and let the frontend's activity POST take over.
	s := &rfbClientStream{}
	require.False(t, s.Consume(append([]byte("RFB 003.008\n"), 1, 1)))
	require.True(t, s.ParsingEnabled())

	require.False(t, s.Consume([]byte{99, 1, 2, 3}))
	require.False(t, s.ParsingEnabled(), "unknown opcode must disable parsing for this connection")

	// Once disabled it stays disabled, and never reports activity again.
	require.False(t, s.Consume(append([]byte{4}, make([]byte, 7)...)))
	require.False(t, s.ParsingEnabled())
}

func TestRFBClientStreamKeepsParsingExtendedClipboard(t *testing.T) {
	// Extended Clipboard reuses ClientCutText with a negative length field.
	// Reading that as unsigned (~4e9) used to Disable() the idle parser for
	// the rest of the connection, so a paste (or the caps handshake) left
	// idle detection on the frontend POST fallback.
	s := &rfbClientStream{}
	require.False(t, s.Consume(append([]byte("RFB 003.008\n"), 1, 1)))

	notify := append([]byte{6, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFC}, 0, 0, 0, 0)
	require.True(t, s.Consume(notify), "extended clipboard is user activity")
	require.True(t, s.ParsingEnabled(), "a signed cut-text length must not disable parsing")

	require.True(t, s.Consume(append([]byte{5}, make([]byte, 5)...)),
		"PointerEvent after an extended paste must still count as activity")
}

func TestRFBClientMessageSizeExtendedClipboardHeaderOnly(t *testing.T) {
	// Incomplete body: wait for 4 payload bytes, do not treat the length as
	// unknown and Disable().
	size, complete, known := rfbClientMessageSize([]byte{6, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFC})
	require.True(t, known)
	require.False(t, complete)
	require.Equal(t, 12, size)
}

func TestRFBClientStreamDoesNotGrowUnbounded(t *testing.T) {
	// A truncated ClientCutText claiming a huge length must not let a client
	// pin arbitrary memory in the relay.
	s := &rfbClientStream{}
	require.False(t, s.Consume(append([]byte("RFB 003.008\n"), 1, 1)))
	// ClientCutText header claiming ~16 MiB, with no body. 0xFFFFFFFF is a
	// signed -1 (extended clipboard of 1 byte) and must NOT take this path.
	s.Consume([]byte{6, 0, 0, 0, 0x00, 0xFF, 0xFF, 0xFF})
	require.False(t, s.ParsingEnabled(),
		"an implausible message length must fail closed rather than buffer")
}
