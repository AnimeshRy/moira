// Package idevice is a thin wrapper around the libimobiledevice CLI tools
// (idevice_id, ideviceinfo, afcclient). Moira only ever READS from the phone:
// nothing here can create, modify or delete files on the device.
package idevice

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Device struct {
	UDID string
	Name string
}

// List returns USB-connected, paired devices.
func List(ctx context.Context) ([]Device, error) {
	out, err := run(ctx, "idevice_id", "-l")
	if err != nil {
		return nil, err
	}
	var devs []Device
	for _, udid := range strings.Fields(out) {
		name, _ := run(ctx, "ideviceinfo", "-u", udid, "-k", "DeviceName")
		devs = append(devs, Device{UDID: udid, Name: strings.TrimSpace(name)})
	}
	return devs, nil
}

// Pull copies remote (a path under the phone's Media directory, e.g.
// "DCIM/100APPLE/IMG_0001.HEIC") to local. afcclient exits 0 even when the
// remote file is missing, so callers must verify the result with Stat/size.
func Pull(ctx context.Context, udid, remote, local string) error {
	out, err := run(ctx, "afcclient", "-u", udid, "get", "/"+strings.TrimPrefix(remote, "/"), local)
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error:") {
		return fmt.Errorf("%s: %s", remote, lastLine(out))
	}
	if _, err := os.Stat(local); err != nil {
		return fmt.Errorf("%s: not transferred", remote)
	}
	return nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		if _, lookErr := exec.LookPath(name); lookErr != nil {
			return "", fmt.Errorf("%s not found: install libimobiledevice (brew install libimobiledevice)", name)
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLine(buf.String()))
	}
	return buf.String(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
