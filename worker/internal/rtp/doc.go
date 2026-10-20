// Package rtp is the pure arithmetic behind a SIP test call's audio
// measurement (call.media): the RTP packet codec (RFC 3550 §5.1), the
// sequence-number machine of RFC 3550 Appendix A.1, the offline statistics
// pass (exact loss, duplicates, reordering, A.8 interarrival jitter, late
// discards under one reference jitter buffer, burstiness), the ITU-T G.107
// E-model estimate, and the G.711 frames a probe sends (silence, and the
// 1004 Hz test tone with its echo marker).
//
// Nothing here does I/O or reads a clock. The worker's media session
// (worker/internal/worker/sip_media.go) records what it sent and received,
// with times as monotonic offsets from its own base so an NTP step cannot
// show up as jitter, and hands the records to Analyze once the readers have
// been joined.
//
// Every float a Report carries is finite and rounded: a single NaN in the
// SIP metrics envelope makes it fail to marshal, which would drop the whole
// envelope (PDD, teardown and every other call fact) along with it.
package rtp
