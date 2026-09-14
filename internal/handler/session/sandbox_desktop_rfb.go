// Package session provides RFB (VNC) client-stream parsing for the sandbox desktop relay.
//
// The relay is transparent — it forwards bytes without rewriting them — but it
// still has to know where client messages start and end, because idle
// detection is by message opcode, not by byte count. Byte counting was
// rejected: the XFCE panel clock, websockify's --heartbeat 30, and
// ContinuousUpdates all keep bytes flowing forever, so a sandbox would never
// be judged idle and its TTL would be refreshed indefinitely.
//
// Everything here is a pure function or a small state machine with no I/O, so
// the parts that are expensive to debug in production are cheap to test.
package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
)

// rfbClientHandshakeBytes is how many bytes the CLIENT sends before the
// message stream begins, under RFB 3.8 with security type None:
//
//	ProtocolVersion   12  ("RFB 003.008\n")
//	security-type      1  (1 = None)
//	ClientInit         1  (shared-flag)
//
// RFB 3.3 would be 13 (the server picks the security type), which is why the
// version assertion below is fail-closed rather than informational.
const rfbClientHandshakeBytes = 14

// rfbProtocolVersion38 is the only server version this relay accepts.
var rfbProtocolVersion38 = []byte("RFB 003.008\n")

// rfbSecurityTypeNone is the only security type x11vnc should offer: it runs
// with -nopw, and authentication lives one hop out, on websockify.
const rfbSecurityTypeNone byte = 1

// rfbMaxClientMessageBytes bounds one buffered client message. The largest
// legitimate one is a paste (ClientCutText); 1 MiB is far beyond any real
// clipboard and keeps a malformed length header from pinning memory.
const rfbMaxClientMessageBytes = 1 << 20

// rfbProtocolVersionBytes is the length of the server's opening message, and
// the most the relay can read before the upgrade: under RFB 3.8 the server
// sends these 12 bytes and then waits for the CLIENT's version before it
// offers anything else (RFC 6143 §7.1.1). The relay must not answer — that
// handshake belongs to noVNC — so reading further here only blocks.
const rfbProtocolVersionBytes = 12

// rfbValidateServerVersion is the assertion that still runs before the
// browser socket is upgraded, so a mismatch is an HTTP status code rather
// than a close frame.
func rfbValidateServerVersion(version []byte) error {
	if !bytes.Equal(version, rfbProtocolVersion38) {
		return fmt.Errorf(
			"rfb: server speaks %q, want %q; the client handshake byte count "+
				"depends on this", version, rfbProtocolVersion38)
	}
	return nil
}

// rfbValidateSecurityTypes keeps the "x11vnc offered None and only None"
// guarantee. It can no longer run pre-upgrade (see rfbProtocolVersionBytes),
// so the relay applies it to the first server message instead.
func rfbValidateSecurityTypes(securityTypes []byte) error {
	if len(securityTypes) != 1 || securityTypes[0] != rfbSecurityTypeNone {
		return fmt.Errorf(
			"rfb: server offered security types %v, want exactly [%d] (None); "+
				"anything else means start-desktop.sh did not run as expected",
			securityTypes, rfbSecurityTypeNone)
	}
	return nil
}

// rfbValidateServerHandshake enforces both fail-closed assertions at once,
// for callers that already hold the whole handshake.
func rfbValidateServerHandshake(version []byte, securityTypes []byte) error {
	if err := rfbValidateServerVersion(version); err != nil {
		return err
	}
	return rfbValidateSecurityTypes(securityTypes)
}

// rfbServerStream applies the security-type assertion to the sandbox→browser
// direction, which is the only place left to make it once the relay stopped
// trying to impersonate the client.
//
// The zero value expects the byte right after the server's ProtocolVersion.
// It inspects exactly one message and then gets out of the way.
type rfbServerStream struct {
	buf  []byte
	done bool
}

// Observe feeds server bytes and reports the first (and only) violation.
func (s *rfbServerStream) Observe(chunk []byte) error {
	if s.done || len(chunk) == 0 {
		return nil
	}
	s.buf = append(s.buf, chunk...)
	// number-of-security-types, then that many single-byte types.
	count := int(s.buf[0])
	if count == 0 {
		// Zero is RFB's "connection failed" form, carrying a reason string
		// rather than an offer. Either way there is no usable desktop here.
		s.done = true
		s.buf = nil
		return errors.New("rfb: server offered no security types")
	}
	if len(s.buf) < 1+count {
		return nil
	}
	types := append([]byte(nil), s.buf[1:1+count]...)
	s.done = true
	s.buf = nil
	return rfbValidateSecurityTypes(types)
}

// rfbClientMessageSize reports the total byte length of the client message at
// the head of buf.
//
//	known    - false for an opcode this relay does not model. The caller MUST
//	           NOT guess a length; see rfbClientStream.Consume.
//	complete - true when buf already holds the whole message.
//	size     - meaningful only when complete is true.
//
// Message layouts are RFC 6143 §7.5 plus the two pseudo-messages libvncserver
// advertises and noVNC therefore sends (150 EnableContinuousUpdates, 248
// ClientFence).
func rfbClientMessageSize(buf []byte) (size int, complete bool, known bool) {
	if len(buf) == 0 {
		return 0, false, true
	}
	switch buf[0] {
	case 0: // SetPixelFormat
		return fixedSize(buf, 20)
	case 2: // SetEncodings: 4-byte header + 4 bytes per encoding
		if len(buf) < 4 {
			return 0, false, true
		}
		n := int(binary.BigEndian.Uint16(buf[2:4]))
		return fixedSize(buf, 4+4*n)
	case 3: // FramebufferUpdateRequest
		return fixedSize(buf, 10)
	case 4: // KeyEvent
		return fixedSize(buf, 8)
	case 5: // PointerEvent
		return fixedSize(buf, 6)
	case 6: // ClientCutText: 8-byte header + payload
		if len(buf) < 8 {
			return 0, false, true
		}
		return fixedSize(buf, 8+rfbClientCutTextPayload(buf[4:8]))
	case 150: // EnableContinuousUpdates
		return fixedSize(buf, 10)
	case 248: // ClientFence: 9-byte header + payload
		if len(buf) < 9 {
			return 0, false, true
		}
		return fixedSize(buf, 9+int(buf[8]))
	case 251: // SetDesktopSize: 8-byte header + 16 bytes per screen
		// Modelled for completeness, but it should never arrive: the
		// frontend pins resizeSession:false in SandboxDesktop.vue. That is a
		// parser contract, not a UX preference — do not turn it on without
		// re-reading this case.
		if len(buf) < 8 {
			return 0, false, true
		}
		return fixedSize(buf, 8+16*int(buf[6]))
	default:
		return 0, false, false
	}
}

// rfbClientCutTextPayload is the payload byte count of a ClientCutText
// message. RFC 6143 uses an unsigned length. The Extended Clipboard
// pseudo-encoding reuses the same opcode with a signed length: negative
// means abs(n) payload bytes. noVNC writes toUnsigned32bit(-data.length);
// reading that as unsigned on amd64 is ~4e9 and Disable()'s idle detection.
func rfbClientCutTextPayload(lengthField []byte) int {
	n := int32(binary.BigEndian.Uint32(lengthField))
	if n >= 0 {
		return int(n)
	}
	if n == math.MinInt32 {
		// -MinInt32 does not fit in int32; not a real clipboard.
		return rfbMaxClientMessageBytes
	}
	return int(-n)
}

func fixedSize(buf []byte, want int) (int, bool, bool) {
	if want < 0 || want > rfbMaxClientMessageBytes {
		// An implausible declared length: treat it like an unknown opcode
		// rather than buffering toward it.
		return 0, false, false
	}
	if len(buf) < want {
		return want, false, true
	}
	return want, true, true
}

// rfbOpcodeCountsAsActivity reports whether a client message means a human
// touched the desktop.
//
// FramebufferUpdateRequest (3) is deliberately excluded: noVNC emits it in
// response to the server's own updates, so a static desktop with a running
// clock produces a steady stream of them.
func rfbOpcodeCountsAsActivity(opcode byte) bool {
	switch opcode {
	case 4, 5, 6: // KeyEvent, PointerEvent, ClientCutText
		return true
	default:
		return false
	}
}

// rfbClientStream tracks one browser→sandbox direction: it skips the 14-byte
// handshake, then splits the message stream and reports user activity.
//
// The zero value is ready to use. It is NOT safe for concurrent use; the relay
// feeds it from a single read goroutine.
type rfbClientStream struct {
	handshakeSeen int
	buf           []byte
	disabled      atomic.Bool
}

// ParsingEnabled reports whether opcode-based activity detection is still
// live. Once it goes false the connection's idle judgement falls back to the
// frontend's activity POST for good. Safe to call from the activity POST
// while Consume runs on the relay goroutine.
func (s *rfbClientStream) ParsingEnabled() bool { return !s.disabled.Load() }

// Disable stops parsing. Exported to the package so the relay can also switch
// to the fallback for reasons outside this file.
func (s *rfbClientStream) Disable() {
	s.disabled.Store(true)
	s.buf = nil
}

// Consume feeds one chunk of client bytes and reports whether it contained at
// least one activity message.
//
// Chunk boundaries carry no meaning: a WebSocket frame may hold several
// messages, and one message may span frames. Both cases are covered by tests.
func (s *rfbClientStream) Consume(chunk []byte) bool {
	if s.disabled.Load() || len(chunk) == 0 {
		return false
	}
	// Skip whatever remains of the handshake.
	if s.handshakeSeen < rfbClientHandshakeBytes {
		need := rfbClientHandshakeBytes - s.handshakeSeen
		if len(chunk) <= need {
			s.handshakeSeen += len(chunk)
			return false
		}
		s.handshakeSeen = rfbClientHandshakeBytes
		chunk = chunk[need:]
	}

	s.buf = append(s.buf, chunk...)
	activity := false
	for len(s.buf) > 0 {
		size, complete, known := rfbClientMessageSize(s.buf)
		if !known {
			// Never guess a length: one wrong split loses framing for the
			// life of the connection.
			s.Disable()
			return false
		}
		if !complete {
			break
		}
		if rfbOpcodeCountsAsActivity(s.buf[0]) {
			activity = true
		}
		s.buf = s.buf[size:]
	}
	// Keep the tail bounded even while waiting for the rest of a message.
	if len(s.buf) > rfbMaxClientMessageBytes {
		s.Disable()
		return false
	}
	return activity
}

var errRFBHandshakeIncomplete = errors.New("rfb: server handshake ended early")

// rfbReadServerVersion drains the server's 12-byte ProtocolVersion from the
// sandbox connection, asserts it, and returns everything it read so the
// caller can replay it to the browser.
//
// It deliberately stops there. Waiting for the security-type list as well
// deadlocks: the server does not send it until the client answers with its
// own version, and the client here is noVNC, which has not been connected
// yet. That bug looked exactly like a dead upstream — the dial 101'd and the
// read then timed out — while the sandbox was serving other clients fine.
func rfbReadServerVersion(next func() ([]byte, error)) ([]byte, error) {
	var buf []byte
	for attempt := 0; attempt < 64; attempt++ {
		if len(buf) >= rfbProtocolVersionBytes {
			if err := rfbValidateServerVersion(buf[:rfbProtocolVersionBytes]); err != nil {
				return nil, err
			}
			return buf, nil
		}
		chunk, err := next()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
	}
	return nil, errRFBHandshakeIncomplete
}

// rfbReadServerHandshake drains the server's opening bytes from the sandbox
// connection, runs the two fail-closed assertions, and returns everything it
// read so the caller can replay it to the browser.
//
// It runs BEFORE the browser socket is upgraded. That ordering is the whole
// reason the assertions can be answered with an HTTP status code instead of a
// close frame the browser shows as a generic disconnect.
//
// next returns the payload of one binary frame from the sandbox, or an error.
func rfbReadServerHandshake(next func() ([]byte, error)) ([]byte, error) {
	var buf []byte
	// Bound the read: a peer that never completes the handshake must not keep
	// us here. 64 frames is far more than the two or three this ever takes.
	for attempt := 0; attempt < 64; attempt++ {
		// ProtocolVersion(12) + number-of-security-types(1) is the minimum
		// before the type list length is even knowable.
		if len(buf) >= 13 {
			need := 13 + int(buf[12])
			if len(buf) >= need {
				if err := rfbValidateServerHandshake(buf[:12], buf[13:need]); err != nil {
					return nil, err
				}
				return buf, nil
			}
		}
		chunk, err := next()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
	}
	return nil, errRFBHandshakeIncomplete
}
