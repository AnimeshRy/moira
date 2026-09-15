package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AnimeshRy/moira/internal/idevice"
	"github.com/AnimeshRy/moira/internal/photos"
)

type progressMsg struct {
	Done, Total int
	Current     string
	Skipped     int
	Errors      []string
	Finished    bool
}

// exportItem is one thing to copy: where it lives on the phone, what to call it
// on disk, how big it should end up, and whether the original is actually on the
// device at all. Photos and documents both reduce to this.
type exportItem struct {
	Remote string
	Name   string
	Bytes  int64
	Local  bool
}

func assetItems(assets []photos.Asset) []exportItem {
	out := make([]exportItem, 0, len(assets))
	for _, a := range assets {
		out = append(out, exportItem{Remote: a.Path(), Name: a.Name, Bytes: a.Bytes, Local: a.Local})
	}
	return out
}

// fileItems converts AFC directory entries. Files on the media partition are by
// definition on the phone, so Local is always true; directories are dropped
// because Moira does not export recursively.
func fileItems(dir string, entries []idevice.Entry) []exportItem {
	var out []exportItem
	for _, e := range entries {
		if e.Dir {
			continue
		}
		out = append(out, exportItem{Remote: e.Path(dir), Name: e.Name, Bytes: e.Bytes, Local: true})
	}
	return out
}

// export copies items from the phone into dir. It never overwrites: a file
// that already exists with the expected size is skipped, one with a different
// size is reported as an error. Downloads land in a ".part" file and are
// renamed only after the size is verified, so a cancelled run leaves no
// half-written photos behind.
func export(ctx context.Context, udid, dir string, items []exportItem, ch chan<- progressMsg) {
	defer close(ch)
	p := progressMsg{Total: len(items)}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.Errors = append(p.Errors, err.Error())
		p.Finished = true
		ch <- p
		return
	}
	for _, a := range items {
		if ctx.Err() != nil {
			break
		}
		p.Current = a.Name
		ch <- p
		if !a.Local {
			p.Errors = append(p.Errors, a.Name+": original is in iCloud only, not on the phone (download it in Photos first)")
			p.Done++
			continue
		}
		dst := filepath.Join(dir, a.Name)
		if st, err := os.Stat(dst); err == nil {
			if st.Size() == a.Bytes || a.Bytes == 0 {
				p.Skipped++
			} else {
				p.Errors = append(p.Errors, fmt.Sprintf("%s: exists locally with different size, left untouched", a.Name))
			}
			p.Done++
			continue
		}
		part := dst + ".part"
		err := idevice.Pull(ctx, udid, a.Remote, part)
		if err == nil {
			if st, serr := os.Stat(part); serr == nil && a.Bytes > 0 && st.Size() != a.Bytes {
				err = fmt.Errorf("%s: size mismatch (got %d, expected %d)", a.Name, st.Size(), a.Bytes)
			}
		}
		if err == nil {
			err = os.Rename(part, dst)
		}
		if err != nil {
			_ = os.Remove(part)
			p.Errors = append(p.Errors, err.Error())
		}
		p.Done++
	}
	p.Finished = true
	ch <- p
}

// safeName makes an album title or remote path segment usable as a directory name.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" || s == "." || s == ".." {
		return "album"
	}
	return s
}
