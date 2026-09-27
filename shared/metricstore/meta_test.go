package metricstore

import "testing"

func TestFormatValue(t *testing.T) {
	cases := []struct {
		v    float64
		u    Unit
		want string
	}{
		{0.942, UnitRatio, "94.2%"},
		{1, UnitRatio, "100%"},
		{1288490188.8, UnitBytes, "1.2 GB"},
		{512, UnitBytes, "512 B"},
		{1536, UnitBytesPerSecond, "1.5 KB/s"},
		{90061, UnitSeconds, "1d 1h"},
		{125, UnitSeconds, "2m 5s"},
		{12.4, UnitCount, "12.4"},
		{10, UnitUnknown, "10"},
	}
	for _, c := range cases {
		if got := FormatValue(c.v, c.u); got != c.want {
			t.Errorf("FormatValue(%v, %d) = %s, want %s", c.v, c.u, got, c.want)
		}
	}
}

func TestLookupAndUnitFor(t *testing.T) {
	if m, ok := Lookup("system.filesystem.utilization"); !ok || m.Unit != UnitRatio || m.Label == "" {
		t.Fatalf("filesystem.utilization lookup = %+v %v", m, ok)
	}
	// Legacy fixed kinds stay renderable.
	if m, ok := Lookup("swap"); !ok || m.Unit != UnitRatio {
		t.Fatalf("legacy swap lookup = %+v %v", m, ok)
	}
	if _, ok := Lookup("myapp.queue.depth"); ok {
		t.Fatal("uncurated metric must not resolve")
	}
	if UnitFor("myapp.custom", "By") != UnitBytes {
		t.Fatal("OTel unit fallback should map By to bytes")
	}
	if UnitFor("myapp.custom", "weird") != UnitUnknown {
		t.Fatal("unknown units stay unknown")
	}
}

func TestDisplayAttr(t *testing.T) {
	if got := DisplayAttr("system.filesystem.utilization", map[string]string{"state": "used", "mountpoint": "/data"}); got != "/data" {
		t.Fatalf("mountpoint should win: %s", got)
	}
	if got := DisplayAttr("system.network.io", map[string]string{"direction": "receive", "device": "eth0"}); got != "receive" {
		t.Fatalf("direction should win over device: %s", got)
	}
	if got := DisplayAttr("system.cpu.utilization", map[string]string{"state": "used"}); got != "" {
		t.Fatalf("bare state must display nothing: %s", got)
	}
}
