package metricstore

import (
	"bytes"
	"testing"
)

func TestCanonicalAttributesSortsKeys(t *testing.T) {
	a := CanonicalAttributes(map[string]string{"mountpoint": "/data", "device": "/dev/sda1"})
	want := `{"device":"/dev/sda1","mountpoint":"/data"}`
	if string(a) != want {
		t.Fatalf("canonical attributes = %s, want %s", a, want)
	}
	if string(CanonicalAttributes(nil)) != "{}" {
		t.Fatalf("nil attributes must canonicalize to {}")
	}
}

func TestAttrHashDistinguishesNameAndAttrs(t *testing.T) {
	base := AttrHash("system.cpu.utilization", map[string]string{"state": "used"})
	if !bytes.Equal(base, AttrHash("system.cpu.utilization", map[string]string{"state": "used"})) {
		t.Fatal("hash must be deterministic")
	}
	if bytes.Equal(base, AttrHash("system.cpu.utilization", map[string]string{"state": "idle"})) {
		t.Fatal("different attributes must hash differently")
	}
	if bytes.Equal(base, AttrHash("system.memory.utilization", map[string]string{"state": "used"})) {
		t.Fatal("different metric names must hash differently")
	}
	// The NUL separator prevents name/attr boundary collisions.
	if bytes.Equal(AttrHash("a", map[string]string{"b": "c"}), AttrHash("ab", map[string]string{"": "c"})) {
		t.Fatal("name/attribute boundary must be unambiguous")
	}
}

func TestSeriesKeyStringRoundTrip(t *testing.T) {
	key := SeriesKeyString("system.filesystem.utilization", map[string]string{
		"mountpoint": "/data", "device": "/dev/sda1", "state": "used",
	})
	want := "system.filesystem.utilization{device=/dev/sda1,mountpoint=/data,state=used}"
	if key != want {
		t.Fatalf("series key = %s, want %s", key, want)
	}

	name, attrs := ParseSeriesKeyString(key)
	if name != "system.filesystem.utilization" {
		t.Fatalf("parsed name = %s", name)
	}
	if attrs["mountpoint"] != "/data" || attrs["device"] != "/dev/sda1" || attrs["state"] != "used" {
		t.Fatalf("parsed attrs = %v", attrs)
	}

	// Legacy alert keys are plain names.
	name, attrs = ParseSeriesKeyString("cpu")
	if name != "cpu" || len(attrs) != 0 {
		t.Fatalf("legacy key parse = %s %v", name, attrs)
	}

	if SeriesKeyString("system.uptime", nil) != "system.uptime" {
		t.Fatal("attribute-less series key must be the bare name")
	}
}
