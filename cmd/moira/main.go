// Moira: browse and export photos & videos from an iPhone, album by album.
// Read-only towards the phone. Requires libimobiledevice (idevice_id,
// ideviceinfo, afcclient) and a paired device.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AnimeshRy/moira/internal/idevice"
	"github.com/AnimeshRy/moira/internal/tui"
)

func main() {
	home, _ := os.UserHomeDir()
	udid := flag.String("udid", "", "device UDID (default: the only connected device)")
	out := flag.String("out", filepath.Join(home, "Pictures", "Moira"), "export root directory")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: moira [flags] [devices|info]\n\n  moira          open the interactive browser\n  moira devices  list connected devices\n  moira info     print device, storage and battery details\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := run(*udid, *out, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "moira:", err)
		os.Exit(1)
	}
}

func run(udid, out, cmd string) error {
	devs, err := idevice.List(context.Background())
	if err != nil {
		return err
	}
	if cmd == "devices" {
		for _, d := range devs {
			fmt.Printf("%s\t%s\n", d.UDID, d.Name)
		}
		return nil
	}
	var dev idevice.Device
	switch {
	case len(devs) == 0:
		return fmt.Errorf("no device found: connect the iPhone over USB, unlock it and tap Trust")
	case udid != "":
		for _, d := range devs {
			if d.UDID == udid {
				dev = d
			}
		}
		if dev.UDID == "" {
			return fmt.Errorf("device %s not connected", udid)
		}
	case len(devs) > 1:
		d, perr := tui.PickDevice(devs)
		if perr != nil {
			return fmt.Errorf("%d devices connected, pick one with -udid (see `moira devices`)", len(devs))
		}
		dev = d
	default:
		dev = devs[0]
	}
	if cmd == "info" {
		return printInfo(dev)
	}
	m, err := tea.NewProgram(tui.New(dev, out), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	return m.(tui.Model).Err()
}

func printInfo(dev idevice.Device) error {
	i, err := idevice.Stat(context.Background(), dev.UDID)
	if err != nil {
		return err
	}
	fmt.Printf("Device     %s (%s, iOS %s, model %s)\nSerial     %s\nUDID       %s\n\n",
		i.Name, i.ProductType, i.IOSVersion, i.Model, i.Serial, dev.UDID)
	fmt.Printf("Capacity   %s\nUsed       %s\nFree       %s\n", gb(i.DiskCapacity), gb(i.Used), gb(i.DataFree))
	for _, c := range i.Categories {
		fmt.Printf("  %-28s %s\n", c.Label, gb(c.Bytes))
	}
	fmt.Printf("  %-28s %s\n\n", "Other (apps, system, caches)", gb(i.Other))
	fmt.Printf("Battery    %d%% charge, health %d%%, %d cycles, %d mAh design\n",
		i.BatteryPct, i.HealthPct, i.CycleCount, i.DesignCapacity)
	return nil
}

func gb(b int64) string { return fmt.Sprintf("%.2f GB", float64(b)/(1<<30)) }
