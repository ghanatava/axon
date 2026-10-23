package h2demux

import "testing"

func TestPhase4EvaluatorTimestampPropagation(t *testing.T) {
	d, _ := New(Config{})
	key := Connection{PID: 1, Conn: 2}
	// Two coalesced frames, first split across TLS reads. Earliest and latest
	// observations must follow the bytes of each frame, not the feed operation.
	first := []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}
	second := []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}
	got, err := d.FeedAt(key, Read, first[:7], 10, 20)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	got, err = d.FeedAt(key, Read, append(first[7:], second...), 30, 40)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].StartedNS != 10 || got[0].FinishedNS != 40 || got[1].StartedNS != 30 || got[1].FinishedNS != 40 {
		t.Fatal(got)
	}
}
