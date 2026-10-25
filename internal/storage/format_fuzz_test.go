package storage

import "testing"

func FuzzParseNeverPanics(f *testing.F) {
	f.Add([]byte("---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nbody"))
	f.Add([]byte{0xef, 0xbb, 0xbf, '-', '-', '-'})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
