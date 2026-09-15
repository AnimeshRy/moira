package tui

import (
	"errors"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AnimeshRy/moira/internal/idevice"
)

type deviceItem struct{ idevice.Device }

func (d deviceItem) Title() string       { return d.Name }
func (d deviceItem) Description() string { return d.UDID }
func (d deviceItem) FilterValue() string { return d.Name + " " + d.UDID }

type picker struct {
	l      list.Model
	choice idevice.Device
}

func (p picker) Init() tea.Cmd { return nil }

func (p picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.l.SetSize(msg.Width-4, msg.Height-4)
		return p, nil
	case tea.KeyMsg:
		// While the list's filter is open those keys belong to the text input.
		if p.l.FilterState() != list.Filtering {
			switch msg.String() {
			case "enter":
				if it, okItem := p.l.SelectedItem().(deviceItem); okItem {
					p.choice = it.Device
				}
				return p, tea.Quit
			case "q", "esc", "ctrl+c":
				return p, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	p.l, cmd = p.l.Update(msg)
	return p, cmd
}

func (p picker) View() string { return appBox.Render(p.l.View()) }

// PickDevice asks which of several connected devices to use. It returns an
// error if the user quits without choosing, or if there is no terminal to draw
// on (piped stdin), so callers can fall back to requiring -udid.
func PickDevice(devs []idevice.Device) (idevice.Device, error) {
	items := make([]list.Item, len(devs))
	for i, d := range devs {
		items[i] = deviceItem{d}
	}
	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Connected devices"
	l.Styles.Title = title
	m, err := tea.NewProgram(picker{l: l}, tea.WithAltScreen()).Run()
	if err != nil {
		return idevice.Device{}, err
	}
	if p := m.(picker); p.choice.UDID != "" {
		return p.choice, nil
	}
	return idevice.Device{}, errors.New("no device selected")
}
