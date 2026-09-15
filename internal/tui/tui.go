// Package tui is Moira's interactive terminal UI: pick a device, pick an
// album, tick the photos/videos you want, confirm, export. Read-only against
// the phone; every copy to disk is preceded by an explicit confirmation.
package tui

import (
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AnimeshRy/moira/internal/idevice"
	"github.com/AnimeshRy/moira/internal/photos"
)

var (
	accent = lipgloss.Color("#C084FC")
	muted  = lipgloss.Color("#6B7280")
	green  = lipgloss.Color("#34D399")
	red    = lipgloss.Color("#F87171")
	amber  = lipgloss.Color("#FBBF24")
	title  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).Background(accent).Padding(0, 1)
	dim    = lipgloss.NewStyle().Foreground(muted)
	ok     = lipgloss.NewStyle().Foreground(green)
	bad    = lipgloss.NewStyle().Foreground(red)
	warn   = lipgloss.NewStyle().Foreground(amber).Bold(true)
	box    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2)
	appBox = lipgloss.NewStyle().Padding(1, 2)
)

type screen int

const (
	scrLoading screen = iota
	scrHome
	scrAlbums
	scrAssets
	scrFiles
	scrConfirm
	scrExporting
	scrDone
)

// docRoots are the AFC directories on the media partition that hold documents
// rather than photos. This is everything plain AFC exposes without asking the
// phone for an app container.
var docRoots = []string{"/Books", "/Downloads", "/Recordings", "/Podcasts"}

type albumItem struct{ photos.Album }

func (a albumItem) Title() string { return a.Album.Title }
func (a albumItem) Description() string {
	return fmt.Sprintf("%d items · %s", a.Count, human(a.Bytes))
}
func (a albumItem) FilterValue() string { return a.Album.Title }

type assetItem struct {
	photos.Asset
	selected bool
}

func (a assetItem) FilterValue() string { return a.Name }

type assetDelegate struct{}

func (assetDelegate) Height() int                         { return 1 }
func (assetDelegate) Spacing() int                        { return 0 }
func (assetDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (assetDelegate) Render(w io.Writer, m list.Model, idx int, it list.Item) {
	a := it.(assetItem)
	check := dim.Render("[ ]")
	if a.selected {
		check = ok.Render("[✓]")
	}
	kind := "photo"
	if a.Video {
		kind = "video " + a.Duration.Round(time.Second).String()
	}
	fav := " "
	if a.Favorite {
		fav = warn.Render("♥")
	}
	where := ""
	if !a.Local {
		where = bad.Render("  ☁ iCloud only")
	}
	line := fmt.Sprintf("%s %s %-28s %-16s %9s  %s%s", check, fav, a.Name, kind, human(a.Bytes), a.Created.Local().Format("2006-01-02 15:04"), where)
	if idx == m.Index() {
		line = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("▶ " + line)
	} else {
		line = "  " + line
	}
	fmt.Fprint(w, line)
}

type fileItem struct {
	idevice.Entry
	dir      string // the directory it was listed from
	selected bool
}

func (f fileItem) FilterValue() string { return f.Entry.Name }

// fileDelegate is deliberately separate from assetDelegate: that one does an
// unchecked type assertion to assetItem and would panic here.
type fileDelegate struct{}

func (fileDelegate) Height() int                         { return 1 }
func (fileDelegate) Spacing() int                        { return 0 }
func (fileDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (fileDelegate) Render(w io.Writer, m list.Model, idx int, it list.Item) {
	f, isFile := it.(fileItem)
	if !isFile {
		return
	}
	check := dim.Render("[ ]")
	switch {
	case f.Dir:
		check = dim.Render(" · ")
	case f.selected:
		check = ok.Render("[✓]")
	}
	name, size := f.Entry.Name, human(f.Bytes)
	if f.Dir {
		name += "/"
		size = dim.Render("   dir")
	}
	when := ""
	if !f.Modified.IsZero() {
		when = f.Modified.Local().Format("2006-01-02 15:04")
	}
	line := fmt.Sprintf("%s %-40s %10s  %s", check, name, size, when)
	if idx == m.Index() {
		line = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("▶ " + line)
	} else {
		line = "  " + line
	}
	fmt.Fprint(w, line)
}

type Model struct {
	ctx    context.Context
	cancel context.CancelFunc
	udid   string
	dev    idevice.Device
	outDir string

	scr    screen
	spin   spinner.Model
	status string
	err    error
	lib    *photos.Library
	albums list.Model
	assets list.Model
	album  photos.Album
	files  list.Model
	cwd    string
	toCopy []exportItem
	dstDir string
	back   screen

	info      idevice.Info
	infoErr   error
	cloudN    int
	cloudSize int64
	prog      progress.Model
	last      progressMsg
	ch        chan progressMsg
	w, h      int
}

type libMsg struct {
	lib       *photos.Library
	albums    []photos.Album
	info      idevice.Info
	infoErr   error
	cloudN    int
	cloudSize int64
	err       error
}
type filesMsg struct {
	dir     string
	entries []idevice.Entry
	err     error
}
type assetsMsg struct {
	assets []photos.Asset
	err    error
}

// New builds the UI for one device. outDir is the export root; each album is
// exported into its own subdirectory.
func New(dev idevice.Device, outDir string) Model {
	ctx, cancel := context.WithCancel(context.Background())
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(accent)))
	al := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	al.Title = "Albums"
	al.Styles.Title = title
	as := list.New(nil, assetDelegate{}, 0, 0)
	as.Styles.Title = title
	as.SetShowStatusBar(true)
	selectKeys := func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
			key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all/none")),
			key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "export selected")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		}
	}
	as.AdditionalShortHelpKeys = selectKeys
	fl := list.New(nil, fileDelegate{}, 0, 0)
	fl.Styles.Title = title
	fl.SetShowStatusBar(true)
	fl.AdditionalShortHelpKeys = selectKeys
	return Model{
		ctx: ctx, cancel: cancel, udid: dev.UDID, dev: dev, outDir: outDir,
		spin: s, albums: al, assets: as, files: fl, back: scrHome,
		prog:   progress.New(progress.WithGradient("#C084FC", "#34D399")),
		status: "Reading " + dev.Name + " (read-only)…",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		// Device stats are decoration: a phone that withholds them still gets a
		// usable UI, so infoErr is carried, not returned.
		info, infoErr := idevice.Stat(m.ctx, m.udid)
		lib, err := photos.Open(m.ctx, m.udid)
		if err != nil {
			return libMsg{info: info, infoErr: infoErr, err: err}
		}
		albums, err := lib.Albums(m.ctx)
		n, size, cerr := lib.CloudOnly(m.ctx)
		if err == nil {
			err = cerr
		}
		return libMsg{lib: lib, albums: albums, info: info, infoErr: infoErr, cloudN: n, cloudSize: size, err: err}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.albums.SetSize(msg.Width-4, msg.Height-4)
		m.assets.SetSize(msg.Width-4, msg.Height-4)
		m.files.SetSize(msg.Width-4, msg.Height-4)
		m.prog.Width = min(60, msg.Width-10)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case libMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.lib = msg.lib
		items := make([]list.Item, len(msg.albums))
		for i, a := range msg.albums {
			items[i] = albumItem{a}
		}
		m.albums.SetItems(items)
		m.info, m.infoErr, m.cloudN, m.cloudSize = msg.info, msg.infoErr, msg.cloudN, msg.cloudSize
		m.scr = scrHome
		return m, nil
	case assetsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg.assets))
		for i, a := range msg.assets {
			items[i] = assetItem{Asset: a}
		}
		m.assets.Title = m.album.Title
		m.assets.SetItems(items)
		m.assets.ResetSelected()
		m.scr = scrAssets
		return m, nil
	case filesMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.cwd = msg.dir
		items := make([]list.Item, len(msg.entries))
		for i, e := range msg.entries {
			items[i] = fileItem{Entry: e, dir: msg.dir}
		}
		m.files.Title = "Documents · " + msg.dir
		m.files.SetItems(items)
		m.files.ResetSelected()
		m.scr = scrFiles
		return m, nil
	case progressMsg:
		m.last = msg
		if msg.Finished {
			m.scr = scrDone
			return m, nil
		}
		return m, waitProgress(m.ch)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.scr {
	case scrHome:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "p", "1", "enter":
			m.scr = scrAlbums
		case "d", "2":
			m.scr = scrLoading
			m.status = "Listing documents…"
			return m, m.listDir(docRootsDir)
		}
	case scrAlbums:
		if m.albums.FilterState() != list.Filtering {
			switch msg.String() {
			case "q", "esc":
				if m.albums.FilterState() == list.Unfiltered {
					m.scr = scrHome
					return m, nil
				}
			case "enter":
				if it, isAlbum := m.albums.SelectedItem().(albumItem); isAlbum {
					m.album = it.Album
					m.scr = scrLoading
					m.status = "Listing " + it.Album.Title + "…"
					return m, tea.Batch(m.spin.Tick, func() tea.Msg {
						a, err := m.lib.Assets(m.ctx, it.Album)
						return assetsMsg{a, err}
					})
				}
			}
		}
		m.albums, cmd = m.albums.Update(msg)
	case scrAssets:
		if m.assets.FilterState() != list.Filtering {
			switch msg.String() {
			case "esc", "q":
				if m.assets.FilterState() == list.Unfiltered {
					m.scr = scrAlbums
					return m, nil
				}
			case " ":
				if it, isAsset := m.assets.SelectedItem().(assetItem); isAsset {
					it.selected = !it.selected
					return m, m.assets.SetItem(m.assets.GlobalIndex(), it)
				}
			case "a":
				items := m.assets.Items()
				all := true
				for _, it := range items {
					if !it.(assetItem).selected {
						all = false
					}
				}
				for i, it := range items {
					a := it.(assetItem)
					a.selected = !all
					items[i] = a
				}
				return m, m.assets.SetItems(items)
			case "e":
				var sel []photos.Asset
				for _, it := range m.assets.Items() {
					if a := it.(assetItem); a.selected {
						sel = append(sel, a.Asset)
					}
				}
				if len(sel) > 0 {
					m.toCopy = assetItems(sel)
					m.dstDir = filepath.Join(m.outDir, safeName(m.album.Title))
					m.back = scrAssets
					m.scr = scrConfirm
				}
				return m, nil
			}
		}
		m.assets, cmd = m.assets.Update(msg)
	case scrFiles:
		if m.files.FilterState() != list.Filtering {
			switch msg.String() {
			case "esc", "q":
				if m.files.FilterState() != list.Unfiltered {
					break
				}
				if m.cwd == docRootsDir {
					m.scr = scrHome
					return m, nil
				}
				m.scr = scrLoading
				m.status = "Listing documents…"
				return m, m.listDir(parentDir(m.cwd))
			case "enter":
				if it, isFile := m.files.SelectedItem().(fileItem); isFile && it.Dir {
					m.scr = scrLoading
					m.status = "Listing " + it.Entry.Name + "…"
					return m, m.listDir(it.Entry.Path(it.dir))
				}
			case " ":
				if it, isFile := m.files.SelectedItem().(fileItem); isFile && !it.Dir {
					it.selected = !it.selected
					return m, m.files.SetItem(m.files.GlobalIndex(), it)
				}
			case "a":
				items := m.files.Items()
				all := true
				for _, it := range items {
					if f := it.(fileItem); !f.Dir && !f.selected {
						all = false
					}
				}
				for i, it := range items {
					f := it.(fileItem)
					f.selected = !f.Dir && !all
					items[i] = f
				}
				return m, m.files.SetItems(items)
			case "e":
				var sel []idevice.Entry
				for _, it := range m.files.Items() {
					if f := it.(fileItem); f.selected && !f.Dir {
						sel = append(sel, f.Entry)
					}
				}
				if len(sel) > 0 {
					m.toCopy = fileItems(m.cwd, sel)
					m.dstDir = m.docDstDir()
					m.back = scrFiles
					m.scr = scrConfirm
				}
				return m, nil
			}
		}
		m.files, cmd = m.files.Update(msg)
	case scrConfirm:
		switch msg.String() {
		case "y", "Y":
			m.scr = scrExporting
			m.ch = make(chan progressMsg)
			m.last = progressMsg{Total: len(m.toCopy)}
			go export(m.ctx, m.udid, m.dstDir, m.toCopy, m.ch)
			return m, waitProgress(m.ch)
		default:
			m.scr = m.back
		}
	case scrDone:
		m.scr = m.back
	}
	return m, cmd
}

func waitProgress(ch <-chan progressMsg) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return p
	}
}

// docRootsDir is the virtual parent of the AFC document roots. It is not a real
// path on the phone; listDir special-cases it.
const docRootsDir = "/"

// listDir lists a directory on the phone, showing the spinner while it runs.
// Each entry costs an afcclient info call, so even small directories are slow
// enough to be worth an async command.
func (m Model) listDir(dir string) tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		if dir == docRootsDir {
			var roots []idevice.Entry
			for _, r := range docRoots {
				// The listing is thrown away; it is how we learn the root exists.
				if _, err := idevice.Ls(m.ctx, m.udid, r); err != nil {
					continue
				}
				roots = append(roots, idevice.Entry{Name: strings.TrimPrefix(r, "/"), Dir: true})
			}
			return filesMsg{dir: docRootsDir, entries: roots}
		}
		es, err := idevice.Ls(m.ctx, m.udid, dir)
		return filesMsg{dir: dir, entries: es, err: err}
	})
}

func parentDir(dir string) string {
	p := path.Dir(strings.TrimSuffix(dir, "/"))
	if p == "." || p == "/" {
		return docRootsDir
	}
	return p
}

// docDstDir mirrors the phone's directory layout under <out>/Documents.
func (m Model) docDstDir() string {
	parts := []string{m.outDir, "Documents"}
	for _, seg := range strings.Split(strings.Trim(m.cwd, "/"), "/") {
		if seg != "" {
			parts = append(parts, safeName(seg))
		}
	}
	return filepath.Join(parts...)
}

func (m Model) View() string {
	switch m.scr {
	case scrLoading:
		return appBox.Render(fmt.Sprintf("%s %s", m.spin.View(), m.status))
	case scrHome:
		return appBox.Render(m.homeView())
	case scrAlbums:
		return appBox.Render(m.albums.View())
	case scrFiles:
		return appBox.Render(m.files.View())
	case scrAssets:
		return appBox.Render(m.assets.View())
	case scrConfirm:
		var bytes int64
		for _, a := range m.toCopy {
			bytes += a.Bytes
		}
		body := fmt.Sprintf("%s\n\n%s\n  %d items, %s\n  from  %s\n  to    %s\n\n%s\n\n%s",
			title.Render("Export?"),
			warn.Render("Moira will COPY these files to your Mac. Nothing on the iPhone is changed or deleted."),
			len(m.toCopy), human(bytes), m.dev.Name, m.dstDir,
			dim.Render("Existing files are never overwritten (same size = skipped).\n  Items marked ☁ iCloud only are not on the phone and will be reported as failed."),
			"Press "+ok.Render("y")+" to copy, any other key to go back.")
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box.Render(body))
	case scrExporting:
		pct := 0.0
		if m.last.Total > 0 {
			pct = float64(m.last.Done) / float64(m.last.Total)
		}
		body := fmt.Sprintf("%s\n\n%s\n%d / %d  %s\n\n%s",
			title.Render("Exporting to "+m.dstDir),
			m.prog.ViewAs(pct), m.last.Done, m.last.Total, dim.Render(m.last.Current),
			dim.Render("ctrl+c to stop (already-copied files are kept, partial file is removed)"))
		return appBox.Render(body)
	case scrDone:
		copied := m.last.Done - m.last.Skipped - len(m.last.Errors)
		b := &strings.Builder{}
		fmt.Fprintf(b, "%s\n\n  %s copied, %s skipped (already there), %s failed\n  %s\n",
			title.Render("Done"), ok.Render(fmt.Sprint(copied)), dim.Render(fmt.Sprint(m.last.Skipped)),
			bad.Render(fmt.Sprint(len(m.last.Errors))), m.dstDir)
		for i, e := range m.last.Errors {
			if i == 10 {
				fmt.Fprintf(b, "  … %d more\n", len(m.last.Errors)-10)
				break
			}
			fmt.Fprintf(b, "  %s %s\n", bad.Render("✗"), e)
		}
		fmt.Fprintf(b, "\n%s", dim.Render("Verify the files on your Mac before deleting anything on the phone. Any key to continue."))
		return appBox.Render(b.String())
	}
	return ""
}

// homeView is the landing screen: who the phone is, where its storage went, how
// the battery is holding up, and how much of the library is not actually here.
func (m Model) homeView() string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s\n\n", title.Render(" Moira · "+m.dev.Name+" "))
	if m.infoErr != nil {
		fmt.Fprintf(b, "%s\n\n", bad.Render("Device details unavailable: "+m.infoErr.Error()))
	}
	i := m.info

	dev := fmt.Sprintf("%s\n%s %s\n%s %s\n%s %s\n%s %s",
		title.Render(" Device "),
		dim.Render("Name      "), i.Name,
		dim.Render("Model     "), strings.TrimSpace(i.ProductType+"  "+i.Model),
		dim.Render("iOS       "), i.IOSVersion,
		dim.Render("Serial    "), i.Serial)

	st := &strings.Builder{}
	fmt.Fprintf(st, "%s\n%s %s   %s %s   %s %s\n",
		title.Render(" Storage "),
		dim.Render("Capacity"), human(i.DiskCapacity),
		dim.Render("Used"), human(i.Used),
		dim.Render("Free"), ok.Render(human(i.DataFree)))
	rows := append(append([]idevice.Category{}, i.Categories...), idevice.Category{Label: "Other (apps, system, caches)", Bytes: i.Other})
	for _, c := range rows {
		fmt.Fprintf(st, "  %-28s %10s  %s\n", c.Label, human(c.Bytes), storageBar(c.Bytes, i.DataCapacity))
	}
	fmt.Fprintf(st, "  %-28s %10s  %s", "Free", human(i.DataFree), storageBar(i.DataFree, i.DataCapacity))

	bat := fmt.Sprintf("%s\n%s %d%%%s   %s %s   %s %d   %s %d mAh",
		title.Render(" Battery "),
		dim.Render("Charge"), i.BatteryPct, map[bool]string{true: " (charging)"}[i.Charging],
		dim.Render("Health"), healthText(i.HealthPct),
		dim.Render("Cycles"), i.CycleCount,
		dim.Render("Design"), i.DesignCapacity)

	fmt.Fprintf(b, "%s\n%s\n%s\n", box.Render(dev), box.Render(st.String()), box.Render(bat))

	if m.cloudN > 0 {
		fmt.Fprintf(b, "\n%s %d items are %s (~%s if downloaded).\n%s\n",
			warn.Render("☁"), m.cloudN, bad.Render("iCloud only"), human(m.cloudSize),
			dim.Render("  They take almost no space on the phone and cannot be fetched over USB. The rest of\n  the gap in \"Other\" is iOS-managed purgeable cache, which only iOS itself can reclaim."))
	}
	fmt.Fprintf(b, "\n%s", "Press "+ok.Render("p")+" for photos · "+ok.Render("d")+" for documents · "+ok.Render("q")+" to quit")
	return b.String()
}

// storageBar draws a 24-cell proportional bar. Anything non-zero gets at least
// one cell so small categories do not vanish entirely.
func storageBar(b, total int64) string {
	const width = 24
	if total <= 0 || b <= 0 {
		return dim.Render(strings.Repeat("·", width))
	}
	n := int(float64(b) / float64(total) * width)
	if n < 1 {
		n = 1
	}
	if n > width {
		n = width
	}
	return lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("█", n)) + dim.Render(strings.Repeat("·", width-n))
}

func healthText(pct int) string {
	switch {
	case pct == 0:
		return dim.Render("unknown")
	case pct >= 80:
		return ok.Render(fmt.Sprintf("%d%%", pct))
	default:
		return bad.Render(fmt.Sprintf("%d%%", pct))
	}
}

// Err reports a fatal error that ended the UI, if any.
func (m Model) Err() error { return m.err }

func human(b int64) string {
	const unit = 1024.0
	f := float64(b)
	for _, s := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < unit {
			return fmt.Sprintf("%.1f %s", f, s)
		}
		f /= unit
	}
	return fmt.Sprintf("%.1f PB", f)
}
