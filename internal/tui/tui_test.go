package tui

import (
	"testing"

	"github.com/AnimeshRy/moira/internal/idevice"
)

func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{
		"/Books/Managed": "/Books",
		"/Books":         docRootsDir,
		"/Books/":        docRootsDir,
		docRootsDir:      docRootsDir,
		"/a/b/c":         "/a/b",
	} {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDocDstDir(t *testing.T) {
	for cwd, want := range map[string]string{
		"/Books":         "/out/Documents/Books",
		"/Books/Managed": "/out/Documents/Books/Managed",
		docRootsDir:      "/out/Documents",
		"/a:b/c*d":       "/out/Documents/a_b/c_d", // safeName applied per segment
	} {
		m := Model{outDir: "/out", cwd: cwd}
		if got := m.docDstDir(); got != want {
			t.Errorf("docDstDir(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestFileItemsSkipsDirs(t *testing.T) {
	got := fileItems("/Books", []idevice.Entry{
		{Name: "Managed", Dir: true},
		{Name: "a.pdf", Bytes: 10},
	})
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1 (directories are not exported)", len(got))
	}
	if got[0].Remote != "/Books/a.pdf" || got[0].Bytes != 10 || !got[0].Local {
		t.Errorf("got %+v", got[0])
	}
}
