package tui

import (
	"os"
	"testing"

	"github.com/AnimeshRy/moira/internal/idevice"
)

// TestRenderHome is a golden-ish eyeball test: run it with -v to see the home
// screen without a phone attached. It only asserts the screen mentions each
// section, so it fails if a panel is dropped.
func TestRenderHome(t *testing.T) {
	m := Model{
		dev: idevice.Device{Name: "Animesh's iPhone"},
		info: idevice.Info{
			Name: "Animesh's iPhone", Model: "MTP03", ProductType: "iPhone15,4",
			IOSVersion: "26.4.2", Serial: "XXXXXXXXXX",
			DiskCapacity: 128000000000, DataCapacity: 120095870976, DataFree: 7414611968,
			Used: 112681259008, Other: 69700000000,
			Categories: []idevice.Category{
				{Label: "Photos & Camera", Bytes: 42979045441},
				{Label: "Calendar", Bytes: 2674688},
				{Label: "Media cache", Bytes: 4096},
			},
			BatteryPct: 90, Charging: true,
			HealthPct: 100, CycleCount: 857, DesignCapacity: 3329,
		},
		cloudN: 184, cloudSize: 891752448,
	}
	out := m.homeView()
	for _, want := range []string{"Device", "Storage", "Battery", "iCloud only", "for photos", "for documents"} {
		if !contains(out, want) {
			t.Errorf("home screen is missing %q", want)
		}
	}
	if os.Getenv("MOIRA_RENDER") != "" {
		t.Log("\n" + out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
