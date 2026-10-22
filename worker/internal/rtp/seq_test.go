package rtp

import "testing"

type seqStep struct {
	seq   uint16
	ext   int64
	epoch int
	ok    bool
}

func runSequence(t *testing.T, name string, steps []seqStep) {
	t.Helper()
	var s Sequence
	for i, st := range steps {
		ext, epoch, ok := s.Update(st.seq)
		if ok != st.ok || ok && (ext != st.ext || epoch != st.epoch) {
			t.Fatalf("%s, step %d (seq %d): ext %d epoch %d ok %v, want %d %d %v",
				name, i, st.seq, ext, epoch, ok, st.ext, st.epoch, st.ok)
		}
	}
}

func TestSequence(t *testing.T) {
	cases := []struct {
		name  string
		steps []seqStep
	}{
		{"probation: the first packet serves it", []seqStep{
			{seq: 100}, {seq: 101, ext: 101, ok: true}, {seq: 102, ext: 102, ok: true},
		}},
		{"one stray is not a stream", []seqStep{
			{seq: 500}, {seq: 1000}, {seq: 1001, ext: 1001, ok: true}, {seq: 1002, ext: 1002, ok: true},
		}},
		{"wrap 65534 to 1", []seqStep{
			{seq: 65533}, {seq: 65534, ext: 65534, ok: true}, {seq: 65535, ext: 65535, ok: true},
			{seq: 0, ext: 65536, ok: true}, {seq: 1, ext: 65537, ok: true},
		}},
		{"a gap below MaxDropout is loss, not a resync", []seqStep{
			{seq: 10}, {seq: 11, ext: 11, ok: true}, {seq: 3010, ext: 3010, ok: true},
		}},
		{"a jump beyond MaxDropout resyncs once confirmed", []seqStep{
			{seq: 10}, {seq: 11, ext: 11, ok: true}, {seq: 12, ext: 12, ok: true},
			{seq: 5000}, {seq: 5001, ext: 5001, epoch: 1, ok: true}, {seq: 5002, ext: 5002, epoch: 1, ok: true},
		}},
		{"an unconfirmed jump is dropped and the stream goes on", []seqStep{
			{seq: 10}, {seq: 11, ext: 11, ok: true}, {seq: 40000}, {seq: 12, ext: 12, ok: true},
		}},
		{"misorder within MaxMisorder counts", []seqStep{
			{seq: 100}, {seq: 101, ext: 101, ok: true}, {seq: 103, ext: 103, ok: true},
			{seq: 102, ext: 102, ok: true}, {seq: 104, ext: 104, ok: true},
		}},
		{"a late packet from before the wrap keeps its cycle", []seqStep{
			{seq: 65534}, {seq: 65535, ext: 65535, ok: true}, {seq: 1, ext: 65537, ok: true},
			{seq: 0, ext: 65536, ok: true}, {seq: 65535, ext: 65535, ok: true},
		}},
		{"a duplicate of the highest counts with the same number", []seqStep{
			{seq: 7}, {seq: 8, ext: 8, ok: true}, {seq: 8, ext: 8, ok: true}, {seq: 9, ext: 9, ok: true},
		}},
		{"a packet further behind than MaxMisorder is dropped", []seqStep{
			{seq: 1000}, {seq: 1001, ext: 1001, ok: true}, {seq: 1300, ext: 1300, ok: true},
			{seq: 1100}, {seq: 1301, ext: 1301, ok: true},
		}},
	}
	for _, c := range cases {
		runSequence(t, c.name, c.steps)
	}
}
