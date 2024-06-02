package main

import (
    "bytes"
    "fmt"
    "io"
    "os"
    "os/exec"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/bubbles/list"
    "github.com/charmbracelet/bubbles/viewport"
    "github.com/charmbracelet/bubbles/textinput"
    "github.com/charmbracelet/lipgloss"
    lua "github.com/yuin/gopher-lua"
    fuzzyfinder "github.com/ktr0731/go-fuzzyfinder"
)

var (
    docStyle         = lipgloss.NewStyle().Margin(1, 2)
    normalBorder     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)
    focusedBorder    = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).Padding(1).BorderForeground(lipgloss.Color("205")) // Pink color
    activeButton     = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("205")) // Pink background
    inactiveButton   = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("62"))
    helpText         = "Press tab to switch focus. Press enter to execute the command. Press q to quit. Press / to filter the output. Press ctrl+l to refresh."
)

type focusState int

const (
    focusList focusState = iota
    focusViewport
    focusInput
)

type command struct {
    name string
    cmd  []string
}

type dimensions struct {
    width  int
    height int
}

type model struct {
    list           list.Model
    viewport       viewport.Model
    input          textinput.Model
    output         string
    focus          focusState
    commands       []command
    showHelp       bool
    vpDimensions   dimensions
    listDimensions dimensions
    tiDimensions   dimensions
    completions    []string
    currentIndex   int
}

func loadConfig() ([]command, dimensions, dimensions, dimensions, []string, error) {
    L := lua.NewState()
    defer L.Close()

    if err := L.DoFile("config.lua"); err != nil {
        return nil, dimensions{}, dimensions{}, dimensions{}, nil, err
    }

    // Get the table returned by the Lua script
    luaTable := L.Get(-1).(*lua.LTable)
    buttonsTable := luaTable.RawGetString("buttons").(*lua.LTable)
    var commands []command

    // Iterate over the Lua table and collect button names and commands
    buttonsTable.ForEach(func(key lua.LValue, value lua.LValue) {
        buttonTable := value.(*lua.LTable)
        name := buttonTable.RawGetString("name").String()
        cmdTable := buttonTable.RawGetString("cmd").(*lua.LTable)

        var cmd []string
        cmdTable.ForEach(func(_, cmdValue lua.LValue) {
            cmd = append(cmd, cmdValue.String())
        })

        commands = append(commands, command{name, cmd})
    })

    // Get viewport dimensions
    vpTable := luaTable.RawGetString("viewport").(*lua.LTable)
    vpWidth := int(vpTable.RawGetString("width").(lua.LNumber))
    vpHeight := int(vpTable.RawGetString("height").(lua.LNumber))

    // Get list dimensions
    listTable := luaTable.RawGetString("list").(*lua.LTable)
    listWidth := int(listTable.RawGetString("width").(lua.LNumber))
    listHeight := int(listTable.RawGetString("height").(lua.LNumber))

    // Get text input dimensions
    tiTable := luaTable.RawGetString("textinput").(*lua.LTable)
    tiWidth := int(tiTable.RawGetString("width").(lua.LNumber))
    tiHeight := 1  // Fixed height for text input

    // Get completions
    completionsTable := luaTable.RawGetString("completions").(*lua.LTable)
    var completions []string
    completionsTable.ForEach(func(key lua.LValue, value lua.LValue) {
        completions = append(completions, value.String())
    })

    return commands, dimensions{width: vpWidth, height: vpHeight}, dimensions{width: listWidth, height: listHeight}, dimensions{width: tiWidth, height: tiHeight}, completions, nil
}

func initialModel(commands []command, vpDimensions, listDimensions, tiDimensions dimensions, completions []string) model {
    items := make([]list.Item, len(commands))
    for i, cmd := range commands {
        items[i] = listItem{cmd.name}
    }

    delegate := customDelegate{}
    l := list.New(items, delegate, listDimensions.width, listDimensions.height)
    l.Title = "Buttons"
    l.SetShowStatusBar(false)
    l.SetFilteringEnabled(false)
    l.SetShowHelp(false)

    vpHeight := vpDimensions.height - tiDimensions.height - 4  // Adjust for text input height and border
    vp := viewport.New(vpDimensions.width, vpHeight)
    vp.SetContent("Output will be displayed here...")
    vp.MouseWheelEnabled = true

    ti := textinput.New()
    ti.Placeholder = "Type a command..."
    ti.Focus()
    ti.Width = tiDimensions.width

    return model{
        list:           l,
        viewport:       vp,
        input:          ti,
        focus:          focusList,
        commands:       commands,
        showHelp:       false,
        vpDimensions:   vpDimensions,
        listDimensions: listDimensions,
        tiDimensions:   tiDimensions,
        completions:    completions,
        currentIndex:   -1,
    }
}

func (m model) Init() tea.Cmd {
    return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "ctrl+n":
            if m.focus == focusList {
                m.focus = focusViewport
            } else if m.focus == focusViewport {
                m.focus = focusInput
            } else {
                m.focus = focusList
            }
        case "ctrl+p":
            if m.focus == focusList {
                m.focus = focusInput
            } else if m.focus == focusViewport {
                m.focus = focusList
            } else {
                m.focus = focusViewport
            }
        case "q":
            return m, tea.Quit
        case "?":
            m.showHelp = !m.showHelp
        case "ctrl+l":
            if m.focus == focusViewport {
                m.viewport.SetContent(m.output)
                m.viewport.GotoBottom()
            }
        }

        if m.focus == focusList {
            switch msg.String() {
            case "enter":
                idx := m.list.Index()
                if idx >= 0 && idx < len(m.commands) {
                    cmd := m.commands[idx]
                    output, err := runCommand(cmd.cmd)
                    if err != nil {
                        m.output += fmt.Sprintf("%s: %s\n", cmd.name, err)
                    } else {
                        m.output += fmt.Sprintf("%s: %s\n", cmd.name, output)
                    }
                    m.viewport.SetContent(m.output)
                    m.viewport.GotoBottom()
                }
            }
        } else if m.focus == focusViewport {
            switch msg.String() {
            case "/":
                m.filterOutput()
            }
        } else if m.focus == focusInput {
            switch msg.String() {
            case "enter":
                inputValue := m.input.Value()
                output, err := runCommand(strings.Fields(inputValue))
                if err != nil {
                    m.output += fmt.Sprintf("%s: %s\n", inputValue, err)
                } else {
                    m.output += fmt.Sprintf("%s: %s\n", inputValue, output)
                }
                m.input.SetValue("")
                m.viewport.SetContent(m.output)
                m.viewport.GotoBottom()
                m.currentIndex = -1
            case "tab":
                if len(m.completions) > 0 {
                    m.currentIndex = (m.currentIndex + 1) % len(m.completions)
                    m.input.SetValue(m.completions[m.currentIndex])
                }
            }
        }
    }

    if m.focus == focusList {
        var listCmd tea.Cmd
        m.list, listCmd = m.list.Update(msg)
        cmds = append(cmds, listCmd)
    } else if m.focus == focusInput {
        var inputCmd tea.Cmd
        m.input, inputCmd = m.input.Update(msg)
        cmds = append(cmds, inputCmd)
    } else {
        var viewportCmd tea.Cmd
        m.viewport, viewportCmd = m.viewport.Update(msg)
        cmds = append(cmds, viewportCmd)
    }

    return m, tea.Batch(cmds...)
}

func (m *model) filterOutput() {
    lines := strings.Split(m.output, "\n")
    idx, err := fuzzyfinder.Find(
        lines,
        func(i int) string {
            return lines[i]
        },
    )
    if err == nil {
        filteredLine := lines[idx]
        m.viewport.SetContent(filteredLine)
    }
}

func (m model) View() string {
    var listStyle, viewportStyle, inputStyle lipgloss.Style
    if m.focus == focusList {
        listStyle = focusedBorder
        viewportStyle = normalBorder
        inputStyle = normalBorder
    } else if m.focus == focusViewport {
        listStyle = normalBorder
        viewportStyle = focusedBorder
        inputStyle = normalBorder
    } else {
        listStyle = normalBorder
        viewportStyle = normalBorder
        inputStyle = focusedBorder
    }

    listView := listStyle.Render(m.list.View())
    viewportView := viewportStyle.Render(m.viewport.View())
    inputView := inputStyle.Render(m.input.View())

    var helpView string
    if m.showHelp {
        helpView = "\n\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(helpText)
    }

    return docStyle.Render(
        lipgloss.JoinHorizontal(
            lipgloss.Top,
            listView,
            lipgloss.JoinVertical(
                lipgloss.Left,
                viewportView,
                inputView,
            ),
        ),
    ) + helpView
}

func runCommand(cmd []string) (string, error) {
    if len(cmd) == 0 {
        return "", fmt.Errorf("empty command")
    }

    c := exec.Command(cmd[0], cmd[1:]...)
    var out bytes.Buffer
    c.Stdout = &out
    c.Stderr = &out

    if err := c.Run(); err != nil {
        return "", err
    }

    return out.String(), nil
}

type listItem struct {
    title string
}

func (i listItem) Title() string       { return i.title }
func (i listItem) Description() string { return "" }
func (i listItem) FilterValue() string { return i.title }

type customDelegate struct{}

func (d customDelegate) Height() int                               { return 1 }
func (d customDelegate) Spacing() int                              { return 0 }
func (d customDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }
func (d customDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
    i, ok := item.(listItem)
    if !ok {
        return
    }

    button := inactiveButton.Render(i.Title())
    if m.Index() == index {
        button = activeButton.Render(i.Title())
    }

    fmt.Fprintf(w, "%s", button)
}

func main() {
    commands, vpDimensions, listDimensions, tiDimensions, completions, err := loadConfig()
    if err != nil {
        fmt.Printf("Error loading config: %v\n", err)
        os.Exit(1)
    }

    p := tea.NewProgram(initialModel(commands, vpDimensions, listDimensions, tiDimensions, completions))
    if err := p.Start(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}
