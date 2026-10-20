//go:build unix

package worker

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

// The media sockets refuse broadcast: Go turns SO_BROADCAST on for every
// datagram socket, and a far end naming its subnet's broadcast address would
// otherwise get the probe's stream sent to every host on the segment.
func TestSIPRTPPair_BroadcastOff(t *testing.T) {
	pair, err := newSIPRTPPorts().bind(net.IPv4zero)
	if err != nil {
		t.Fatal(err)
	}
	defer pair.Close()
	for name, conn := range map[string]*net.UDPConn{"rtp": pair.rtp, "rtcp": pair.rtcp} {
		raw, err := conn.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var v int
		var gerr error
		if err := raw.Control(func(fd uintptr) {
			v, gerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST)
		}); err != nil || gerr != nil {
			t.Fatalf("%s: getsockopt: %v %v", name, err, gerr)
		}
		if v != 0 {
			t.Fatalf("%s socket has SO_BROADCAST %d, want 0", name, v)
		}
	}
	// A write to the broadcast address of an attached broadcast-capable
	// subnet is refused, where Go's default would send it. (255.255.255.255
	// is no test: some systems have no route for it at all.)
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagBroadcast == 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ones, bits := ipNet.Mask.Size()
			if bits != 32 || ones > 30 {
				continue
			}
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte(ipNet.IP.To4())), ones)
			dst := net.UDPAddrFromAddrPort(netip.AddrPortFrom(sipBroadcastAddr(p), 40000))
			_, err := pair.rtp.WriteToUDP([]byte{0x80}, dst)
			if !errors.Is(err, syscall.EACCES) {
				t.Fatalf("write to %s (broadcast of %s on %s) = %v, want EACCES", dst, p, iface.Name, err)
			}
			if sipMediaWriteClass(err) != sipMediaWriteEPERM {
				t.Fatalf("a refused broadcast write is classed %d, want eperm", sipMediaWriteClass(err))
			}
		}
	}
}
