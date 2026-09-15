package idevice

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Category is one labelled slice of the phone's data partition.
type Category struct {
	Label string
	Bytes int64
}

// Info is what Moira shows on its home screen: identity, storage breakdown and
// battery health. Every field is best-effort; a phone or an iOS version that
// withholds one of the lockdown domains leaves those fields zero rather than
// failing the whole call.
type Info struct {
	Name, Model, ProductType, IOSVersion, Serial string

	DiskCapacity int64 // whole NAND, marketing size
	DataCapacity int64 // the user data partition
	DataFree     int64
	Used         int64 // DataCapacity - DataFree
	Other        int64 // Used minus the categories below (apps, system, caches)
	Categories   []Category

	BatteryPct int
	Charging   bool

	HealthPct      int // FullChargeCapacity as a percentage of design
	CycleCount     int
	DesignCapacity int // mAh
}

// Stat collects device identity, disk usage and battery health. Only the base
// ideviceinfo query is fatal; the disk_usage, battery and diagnostics queries
// degrade to zero values so a partially cooperative phone still renders.
func Stat(ctx context.Context, udid string) (Info, error) {
	base, err := runKV(ctx, udid)
	if err != nil {
		return Info{}, err
	}
	var i Info
	i.Name = base["DeviceName"]
	i.Model = base["ModelNumber"]
	i.ProductType = base["ProductType"]
	i.IOSVersion = base["ProductVersion"]
	i.Serial = base["SerialNumber"]

	// TotalDataAvailable is stale on modern iOS (it reports free space including
	// what the OS considers purgeable, and on a 128 GB phone holding 43 GB of
	// photos it reported 93 GB free). AmountDataAvailable is the live figure.
	du, _ := runKV(ctx, udid, "-q", "com.apple.disk_usage.factory")
	i.DiskCapacity = num(du["TotalDiskCapacity"])
	i.DataCapacity = num(du["TotalDataCapacity"])
	i.DataFree = num(du["AmountDataAvailable"])
	if i.DataCapacity > 0 {
		i.Used = i.DataCapacity - i.DataFree
	}
	// PhotoUsage and CameraUsage overlap (identical on the reference device), so
	// they collapse into one row rather than being counted twice.
	i.addCategory("Photos & Camera", max(num(du["PhotoUsage"]), num(du["CameraUsage"])))
	i.addCategory("Voicemail", num(du["VoicemailUsage"]))
	i.addCategory("Calendar", num(du["CalendarUsage"]))
	i.addCategory("Notes", num(du["NotesUsage"]))
	i.addCategory("Media cache", num(du["MediaCacheUsage"]))
	i.addCategory("Web cache", num(du["WebAppCacheUsage"]))
	var known int64
	for _, c := range i.Categories {
		known += c.Bytes
	}
	if i.Other = i.Used - known; i.Other < 0 {
		i.Other = 0
	}

	bat, _ := runKV(ctx, udid, "-q", "com.apple.mobile.battery")
	i.BatteryPct = int(num(bat["BatteryCurrentCapacity"]))
	i.Charging = bat["BatteryIsCharging"] == "true"

	// The diagnostics relay can sit there waiting on a device that won't answer.
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, derr := run(dctx, "idevicediagnostics", "-u", udid, "diagnostics", "All"); derr == nil {
		i.CycleCount = plistInt(out, "CycleCount")
		i.DesignCapacity = plistInt(out, "DesignCapacity")
		i.HealthPct = plistInt(out, "FullChargeCapacity")
	}
	return i, nil
}

func (i *Info) addCategory(label string, b int64) {
	if b > 0 {
		i.Categories = append(i.Categories, Category{label, b})
	}
}

func runKV(ctx context.Context, udid string, args ...string) (map[string]string, error) {
	out, err := run(ctx, "ideviceinfo", append([]string{"-u", udid}, args...)...)
	if err != nil {
		return nil, err
	}
	return parseKV(out), nil
}

// parseKV reads ideviceinfo's default "Key: value" output. Non-scalar values
// (NANDInfo's base64 blob, for one) come through as opaque strings and are
// simply never looked up.
func parseKV(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, found := strings.Cut(line, ":")
		if found {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// ponytail: regex scrape of three known integer keys out of idevicediagnostics'
// XML plist. Pull in a real plist decoder if we ever need nested or typed values.
var plistIntRe = regexp.MustCompile(`<key>([A-Za-z]+)</key>\s*<integer>(-?\d+)</integer>`)

func plistInt(xml, key string) int {
	for _, m := range plistIntRe.FindAllStringSubmatch(xml, -1) {
		if m[1] == key {
			n, _ := strconv.Atoi(m[2])
			return n
		}
	}
	return 0
}

func num(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
