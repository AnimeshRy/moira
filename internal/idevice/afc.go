package idevice

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Entry is one name inside an AFC directory on the phone.
type Entry struct {
	Name     string
	Dir      bool
	Bytes    int64
	Modified time.Time
}

// Path is the entry's absolute location on the device.
func (e Entry) Path(dir string) string { return path.Join(abs(dir), e.Name) }

// Ls lists an AFC directory. afcclient's ls gives names only, so each name costs
// one extra info call to learn its size and whether it is a directory.
//
// ponytail: one process per entry. The media-partition document roots hold tens
// of files, not thousands; parallelize if that ever stops being true.
func Ls(ctx context.Context, udid, dir string) ([]Entry, error) {
	dir = abs(dir)
	out, err := run(ctx, "afcclient", "-u", udid, "ls", dir)
	if err != nil {
		return nil, err
	}
	if strings.Contains(out, "Error:") {
		return nil, fmt.Errorf("%s: %s", dir, lastLine(out))
	}
	var entries []Entry
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == "" || name == "." || name == ".." {
			continue
		}
		e, err := stat(ctx, udid, path.Join(dir, name))
		if err != nil {
			continue // unreadable entry: show the rest rather than failing the listing
		}
		e.Name = name
		entries = append(entries, e)
	}
	// Directories first, then files, each alphabetically.
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].Dir != entries[b].Dir {
			return entries[a].Dir
		}
		return strings.ToLower(entries[a].Name) < strings.ToLower(entries[b].Name)
	})
	return entries, nil
}

func stat(ctx context.Context, udid, remote string) (Entry, error) {
	out, err := run(ctx, "afcclient", "-u", udid, "info", remote)
	if err != nil {
		return Entry{}, err
	}
	if strings.Contains(out, "Error:") {
		return Entry{}, fmt.Errorf("%s: %s", remote, lastLine(out))
	}
	return parseInfo(out)
}

func parseInfo(out string) (Entry, error) {
	var raw struct {
		Size  int64  `json:"st_size"`
		Ifmt  string `json:"st_ifmt"`
		Mtime int64  `json:"st_mtime"` // nanoseconds since the Unix epoch
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return Entry{}, fmt.Errorf("afcclient info: %w", err)
	}
	e := Entry{Dir: raw.Ifmt == "S_IFDIR", Bytes: raw.Size}
	if raw.Mtime > 0 {
		e.Modified = time.Unix(0, raw.Mtime)
	}
	return e, nil
}

func abs(p string) string { return "/" + strings.TrimPrefix(path.Clean("/"+p), "/") }
