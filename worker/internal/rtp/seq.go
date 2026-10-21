package rtp

// The constants of RFC 3550 Appendix A.1.
const (
	MinSequential = 2    // packets in sequence before a source is valid
	MaxDropout    = 3000 // largest forward jump taken as loss
	MaxMisorder   = 100  // largest backward step taken as a late packet
	seqMod        = 1 << 16
)

// Sequence is the per-SSRC sequence-number machine of RFC 3550 Appendix A.1
// (update_seq), extended to hand back each packet's extended sequence number
// so the statistics can keep an exact received set instead of A.1's
// running counters. The zero value is ready for a source's first packet.
//
// A new source is on probation until MinSequential packets arrive in
// sequence, so one stray datagram is never a stream; the packets that served
// probation are not counted, as in A.1. A forward jump of MaxDropout or more
// is taken as a restarted sender once a second packet confirms it, which
// starts a new epoch: extended numbers of different epochs are not
// comparable, and the statistics treat each epoch as its own run.
type Sequence struct {
	started   bool
	probation int
	maxSeq    uint16
	badSeq    uint32 // seqMod + 1 when no jump awaits confirmation
	cycles    int64  // wraps seen in this epoch, times seqMod
	epoch     int
}

// Update feeds one packet's sequence number and reports its extended
// sequence number within the current epoch, which epoch that is, and
// whether the packet counts. A packet that does not count (probation, an
// unconfirmed jump, or one too far behind) is left out of every statistic.
func (s *Sequence) Update(seq uint16) (ext int64, epoch int, ok bool) {
	if !s.started {
		s.started = true
		s.init(seq)
		s.maxSeq = seq - 1
		s.probation = MinSequential
	}
	if s.probation > 0 {
		if seq == s.maxSeq+1 {
			s.probation--
			s.maxSeq = seq
			if s.probation == 0 {
				s.init(seq)
				return int64(seq), s.epoch, true
			}
		} else {
			s.probation = MinSequential - 1
			s.maxSeq = seq
		}
		return 0, s.epoch, false
	}

	udelta := seq - s.maxSeq
	switch {
	case udelta < MaxDropout:
		// In order, possibly with a gap; a smaller number means it wrapped.
		if seq < s.maxSeq {
			s.cycles += seqMod
		}
		s.maxSeq = seq
		return s.cycles + int64(seq), s.epoch, true
	case int(udelta) <= seqMod-MaxMisorder:
		// A large jump: a restarted sender if the next packet follows it.
		if uint32(seq) != s.badSeq {
			s.badSeq = uint32(seq+1) & (seqMod - 1)
			return 0, s.epoch, false
		}
		s.epoch++
		s.init(seq)
		return int64(seq), s.epoch, true
	default:
		// A duplicate or a reordered packet, at most MaxMisorder behind.
		// One numerically above the highest so far was sent before the wrap.
		ext = s.cycles + int64(seq)
		if seq > s.maxSeq {
			ext -= seqMod
		}
		return ext, s.epoch, true
	}
}

// init is A.1's init_seq: seq becomes the base of a fresh count.
func (s *Sequence) init(seq uint16) {
	s.maxSeq = seq
	s.badSeq = seqMod + 1
	s.cycles = 0
}
