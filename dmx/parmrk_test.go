package dmx

import (
	"bytes"
	"testing"
)

func TestParmrkDecodeSingleChunk(t *testing.T) {
	tests := []struct {
		name     string
		src      []byte
		wantData []byte
		wantMark Marker
		wantCons int
	}{
		{
			name:     "plain data",
			src:      []byte{0x00, 0x01, 0x02},
			wantData: []byte{0x00, 0x01, 0x02},
			wantMark: MarkerNone,
			wantCons: 3,
		},
		{
			name:     "doubled FF is one literal 0xFF",
			src:      []byte{0xFF, 0xFF},
			wantData: []byte{0xFF},
			wantMark: MarkerNone,
			wantCons: 2,
		},
		{
			name:     "break",
			src:      []byte{0xFF, 0x00, 0x00},
			wantData: []byte{},
			wantMark: MarkerBreak,
			wantCons: 3,
		},
		{
			name:     "framing error on a byte",
			src:      []byte{0xFF, 0x00, 0x42},
			wantData: []byte{},
			wantMark: MarkerError,
			wantCons: 3,
		},
		{
			name:     "data then break, tail unconsumed",
			src:      []byte{0x01, 0x02, 0xFF, 0x00, 0x00, 0x03, 0x04},
			wantData: []byte{0x01, 0x02},
			wantMark: MarkerBreak,
			wantCons: 5,
		},
		{
			name:     "channel value 255 survives round trip",
			src:      []byte{0x00, 0xFF, 0xFF, 0x7F},
			wantData: []byte{0x00, 0xFF, 0x7F},
			wantMark: MarkerNone,
			wantCons: 4,
		},
		{
			name:     "several 255s in one chunk",
			src:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			wantData: []byte{0xFF, 0xFF, 0xFF},
			wantMark: MarkerNone,
			wantCons: 6,
		},
		{
			name: "defensive: FF followed by neither FF nor 00",
			// Not reachable from a conforming tty; must not drop the 0x42.
			src:      []byte{0xFF, 0x42},
			wantData: []byte{0xFF, 0x42},
			wantMark: MarkerNone,
			wantCons: 2,
		},
		{
			name:     "trailing partial escape consumes all, reports nothing",
			src:      []byte{0x01, 0xFF},
			wantData: []byte{0x01},
			wantMark: MarkerNone,
			wantCons: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d parmrkDecoder
			out, m, cons := d.decode(nil, tc.src)
			if !bytes.Equal(out, tc.wantData) {
				t.Errorf("data: got % x, want % x", out, tc.wantData)
			}
			if m != tc.wantMark {
				t.Errorf("marker: got %v, want %v", m, tc.wantMark)
			}
			if cons != tc.wantCons {
				t.Errorf("consumed: got %d, want %d", cons, tc.wantCons)
			}
		})
	}
}

// drain mimics what ReadChunk does: feed chunks through one decoder, carrying
// the unconsumed tail forward, and collect every (data, marker) pair.
func drain(d *parmrkDecoder, carry []byte, chunks [][]byte) (data []byte, marks []Marker) {
	for _, c := range chunks {
		src := append(carry, c...)
		for {
			out, m, cons := d.decode(nil, src)
			data = append(data, out...)
			src = src[cons:]
			if m == MarkerNone {
				break
			}
			marks = append(marks, m)
		}
		carry = src
	}
	return data, marks
}

func TestParmrkDecodeAcrossChunkBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		chunks   [][]byte
		wantData []byte
		wantMark []Marker
	}{
		{
			name:     "break split FF | 00 | 00",
			chunks:   [][]byte{{0x01, 0xFF}, {0x00}, {0x00, 0x02}},
			wantData: []byte{0x01, 0x02},
			wantMark: []Marker{MarkerBreak},
		},
		{
			name:     "break split FF 00 | 00",
			chunks:   [][]byte{{0x01, 0xFF, 0x00}, {0x00, 0x02}},
			wantData: []byte{0x01, 0x02},
			wantMark: []Marker{MarkerBreak},
		},
		{
			name:     "doubled FF split across chunks",
			chunks:   [][]byte{{0x01, 0xFF}, {0xFF, 0x02}},
			wantData: []byte{0x01, 0xFF, 0x02},
			wantMark: nil,
		},
		{
			name:     "two breaks in one chunk",
			chunks:   [][]byte{{0xFF, 0x00, 0x00, 0x01, 0xFF, 0x00, 0x00, 0x02}},
			wantData: []byte{0x01, 0x02},
			wantMark: []Marker{MarkerBreak, MarkerBreak},
		},
		{
			name:     "error then break",
			chunks:   [][]byte{{0xFF, 0x00, 0x42, 0x01, 0xFF, 0x00, 0x00}},
			wantData: []byte{0x01},
			wantMark: []Marker{MarkerError, MarkerBreak},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d parmrkDecoder
			data, marks := drain(&d, nil, tc.chunks)
			if !bytes.Equal(data, tc.wantData) {
				t.Errorf("data: got % x, want % x", data, tc.wantData)
			}
			if len(marks) != len(tc.wantMark) {
				t.Fatalf("markers: got %v, want %v", marks, tc.wantMark)
			}
			for i := range marks {
				if marks[i] != tc.wantMark[i] {
					t.Errorf("marker %d: got %v, want %v", i, marks[i], tc.wantMark[i])
				}
			}
		})
	}
}

// A full DMX frame, byte-for-byte as the tty would present it, is the case that
// actually has to work.
func TestParmrkDecodeFullDMXFrame(t *testing.T) {
	var wire []byte
	wire = append(wire, 0xFF, 0x00, 0x00) // BREAK
	wire = append(wire, 0x00)             // start code
	want := []byte{0x00}
	for i := 0; i < MaxChannels; i++ {
		v := byte(i % 256)
		if v == 0xFF {
			wire = append(wire, 0xFF, 0xFF) // doubled on the wire
		} else {
			wire = append(wire, v)
		}
		want = append(want, v)
	}
	wire = append(wire, 0xFF, 0x00, 0x00) // next BREAK

	var d parmrkDecoder
	data, marks := drain(&d, nil, [][]byte{wire})

	if len(marks) != 2 || marks[0] != MarkerBreak || marks[1] != MarkerBreak {
		t.Fatalf("markers: got %v, want two breaks", marks)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("decoded %d bytes, want %d", len(data), len(want))
	}
}

func FuzzParmrkDecode(f *testing.F) {
	f.Add([]byte{0xFF, 0x00, 0x00})
	f.Add([]byte{0xFF, 0xFF, 0x01, 0x02})
	f.Add([]byte{0xFF, 0x00, 0x42})
	f.Fuzz(func(t *testing.T, src []byte) {
		var d parmrkDecoder
		out, _, cons := d.decode(nil, src)
		if cons > len(src) {
			t.Fatalf("consumed %d > len(src) %d", cons, len(src))
		}
		if len(out) > len(src) {
			t.Fatalf("decoder produced %d bytes from %d: cannot expand", len(out), len(src))
		}
	})
}
