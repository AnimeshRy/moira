package idevice

import (
	"testing"
	"time"
)

// Captured from `ideviceinfo -q com.apple.disk_usage.factory` on a real iPhone.
// NANDInfo is the kind of opaque non-scalar value parseKV must not choke on.
const duFixture = `AmountDataAvailable: 7414611968
AmountDataReserved: 209715200
CalculateDiskUsage: OkilyDokily
CameraUsage: 42979045441
MediaCacheUsage: 4096
NANDInfo: AQAAAAEAAAABAAAAAAAAgKUCAAABAAAACAAA+uqoagCmAgAA
NotesUsage: 0
PhotoUsage: 42979045441
TotalDataCapacity: 120095870976
TotalDiskCapacity: 128000000000
`

func TestParseKV(t *testing.T) {
	m := parseKV(duFixture)
	for k, want := range map[string]string{
		"AmountDataAvailable": "7414611968",
		"PhotoUsage":          "42979045441",
		"NotesUsage":          "0",
		"CalculateDiskUsage":  "OkilyDokily",
		"NANDInfo":            "AQAAAAEAAAABAAAAAAAAgKUCAAABAAAACAAA+uqoagCmAgAA",
	} {
		if got := m[k]; got != want {
			t.Errorf("parseKV[%q] = %q, want %q", k, got, want)
		}
	}
	if _, ok := m["nope"]; ok {
		t.Error("parseKV invented a key")
	}
	if got := num(m["PhotoUsage"]); got != 42979045441 {
		t.Errorf("num = %d", got)
	}
	if got := num(m["CalculateDiskUsage"]); got != 0 {
		t.Errorf("num of a non-number = %d, want 0", got)
	}
}

// Captured from `idevicediagnostics diagnostics All`.
const gasGauge = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>GasGauge</key>
	<dict>
		<key>CycleCount</key>
		<integer>857</integer>
		<key>DesignCapacity</key>
		<integer>3329</integer>
		<key>FullChargeCapacity</key>
		<integer>100</integer>
		<key>Status</key>
		<string>Success</string>
	</dict>
</dict>
</plist>`

func TestPlistInt(t *testing.T) {
	for _, c := range []struct {
		key  string
		want int
	}{
		{"CycleCount", 857},
		{"DesignCapacity", 3329},
		{"FullChargeCapacity", 100},
		{"Status", 0},  // a string value, not an integer
		{"Missing", 0}, // absent
	} {
		if got := plistInt(gasGauge, c.key); got != c.want {
			t.Errorf("plistInt(%q) = %d, want %d", c.key, got, c.want)
		}
	}
}

func TestParseInfo(t *testing.T) {
	dir, err := parseInfo(`{"st_size": 256, "st_blocks": 0, "st_nlink": 6, "st_ifmt": "S_IFDIR", "st_mtime": 1778387121880051197}`)
	if err != nil {
		t.Fatal(err)
	}
	if !dir.Dir || dir.Bytes != 256 {
		t.Errorf("dir = %+v", dir)
	}
	if want := time.Unix(0, 1778387121880051197); !dir.Modified.Equal(want) {
		t.Errorf("Modified = %v, want %v", dir.Modified, want)
	}

	file, err := parseInfo(`{"st_size": 326, "st_ifmt": "S_IFREG", "st_mtime": 1724553136306556290}`)
	if err != nil {
		t.Fatal(err)
	}
	if file.Dir || file.Bytes != 326 {
		t.Errorf("file = %+v", file)
	}

	if _, err := parseInfo("Error: Failed to get file info for /Nope: Not found (8)"); err == nil {
		t.Error("want an error for afcclient's non-JSON failure output")
	}
}

func TestAbs(t *testing.T) {
	for in, want := range map[string]string{
		"Books": "/Books", "/Books": "/Books", "/Books/": "/Books",
		"": "/", "/": "/", "//Books//x": "/Books/x", "/Books/../x": "/x",
	} {
		if got := abs(in); got != want {
			t.Errorf("abs(%q) = %q, want %q", in, got, want)
		}
	}
}
