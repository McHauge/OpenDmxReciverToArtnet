package dmx

// PARMRK decoding.
//
// POSIX has no out-of-band BREAK notification. With PARMRK and INPCK set, and
// IGNBRK/BRKINT/IGNPAR clear, the tty layer escapes line events directly into
// the read stream:
//
//	FF 00 00   BREAK
//	FF 00 XX   framing or parity error on byte XX
//	FF FF      one literal 0xFF data byte
//	any other  itself
//
// A DMX break reported as TTY_BREAK and one reported as a framing error on a
// 0x00 byte encode identically, so we do not care which the driver chose.
//
// This file is deliberately free of syscalls and build tags: it is pure byte
// manipulation, so it compiles and is tested on every platform including
// Windows, where the same Marker vocabulary comes from ClearCommError instead.

type parmrkState uint8

const (
	parmrkIdle    parmrkState = iota // normal data
	parmrkSawFF                      // consumed one 0xFF
	parmrkSawFF00                    // consumed 0xFF 0x00
)

// parmrkDecoder carries escape state across read boundaries, because a 3-byte
// marker can straddle two reads.
type parmrkDecoder struct {
	state parmrkState
}

// reset clears any partial escape sequence. Use it after an error or a flush,
// where a dangling FF would otherwise corrupt the first byte of the next read.
func (d *parmrkDecoder) reset() { d.state = parmrkIdle }

// decode consumes src, appending decoded data bytes to dst.
//
// It stops at the first line event, returning that marker and the number of
// src bytes consumed (including the marker's own bytes). The caller must stash
// src[consumed:] and hand it back on the next call. This "data, then event" cut
// is what makes frame truncation structurally impossible: the bytes before a
// break can never be reported after it.
//
// If src runs out with no event, the marker is MarkerNone and consumed == len(src).
func (d *parmrkDecoder) decode(dst, src []byte) (out []byte, m Marker, consumed int) {
	out = dst
	i := 0
	for i < len(src) {
		b := src[i]
		switch d.state {
		case parmrkIdle:
			if b == 0xFF {
				d.state = parmrkSawFF
				i++
				continue
			}
			out = append(out, b)
			i++

		case parmrkSawFF:
			switch b {
			case 0xFF: // doubled 0xFF: one literal data byte
				out = append(out, 0xFF)
				d.state = parmrkIdle
				i++
			case 0x00: // start of a line-event marker
				d.state = parmrkSawFF00
				i++
			default:
				// Not reachable from a conforming tty: with PARMRK on, a data
				// 0xFF is always doubled. Treat the FF as data and reprocess b
				// from idle rather than dropping a byte.
				out = append(out, 0xFF)
				d.state = parmrkIdle
			}

		case parmrkSawFF00:
			d.state = parmrkIdle
			i++
			if b == 0x00 {
				return out, MarkerBreak, i
			}
			return out, MarkerError, i
		}
	}
	return out, MarkerNone, i
}
