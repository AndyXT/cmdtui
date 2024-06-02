package main

import (
    "bytes"
    "fmt"
    "io"
    "log"
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
    docStyle       = lipgloss.NewStyle().Margin(1, 2)
    normalBorder   = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)
    focusedBorder  = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).Padding(1).BorderForeground(lipgloss.Color("205"))
    activeButton   = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("205"))
    inactiveButton = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("62"))
    helpText       = "Press tab to switch focus. Press enter to execute the command. Press q to quit. Press / to filter the output. Press ctrl+l to refresh."
)

type focusState int

const (
    focusList focusState = iota
    focusViewport
    focusInput
)

type command struct {
    name   string
    cmd    []string
    prompt bool
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

    luaTable := L.Get(-1).(*lua.LTable)
    commands := extractCommands(luaTable.RawGetString("buttons").(*lua.LTable))
    vpDimensions := extractDimensions(luaTable.RawGetString("viewport").(*lua.LTable))
    listDimensions := extractDimensions(luaTable.RawGetString("list").(*lua.LTable))
    tiDimensions := dimensions{width: int(luaTable.RawGetString("textinput").(*lua.LTable).RawGetString("width").(lua.LNumber)), height: 1}
    completions := extractCompletions(luaTable.RawGetString("completions").(*lua.LTable))

    return commands, vpDimensions, listDimensions, tiDimensions, completions, nil
}

func extractCommands(buttonsTable *lua.LTable) []command {
    var commands []command
    buttonsTable.ForEach(func(_, value lua.LValue) {
        buttonTable := value.(*lua.LTable)
        name := buttonTable.RawGetString("name").String()
        cmd := extractCmd(buttonTable.RawGetString("cmd").(*lua.LTable))
        prompt := buttonTable.RawGetString("prompt").(lua.LBool)

        commands = append(commands, command{name, cmd, bool(prompt)})
    })
    return commands
}

func extractCmd(cmdTable *lua.LTable) []string {
    var cmd []string
    cmdTable.ForEach(func(_, cmdValue lua.LValue) {
        cmd = append(cmd, cmdValue.String())
    })
    return cmd
}

func extractDimensions(dimTable *lua.LTable) dimensions {
    return dimensions{
        width:  int(dimTable.RawGetString("width").(lua.LNumber)),
        height: int(dimTable.RawGetString("height").(lua.LNumber)),
    }
}

func extractCompletions(completionsTable *lua.LTable) []string {
    var completions []string
    completionsTable.ForEach(func(_, value lua.LValue) {
        completions = append(completions, value.String())
    })
    return completions
}

func initialModel(commands []command, vpDimensions, listDimensions, tiDimensions dimensions, completions []string) model {
    items := make([]list.Item, len(commands))
    for i, cmd := range commands {
        items[i] = listItem{cmd.name}
    }

    l := list.New(items, customDelegate{}, listDimensions.width, listDimensions.height)
    l.Title = "Buttons"
    l.SetShowStatusBar(false)
    l.SetFilteringEnabled(false)
    l.SetShowHelp(false)

    vp := viewport.New(vpDimensions.width, vpDimensions.height-tiDimensions.height-4)
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
            m.focus = (m.focus + 1) % 3
        case "ctrl+p":
            m.focus = (m.focus + 2) % 3
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

        if m.focus == focusList && msg.String() == "enter" {
            idx := m.list.Index()
            if idx >= 0 && idx < len(m.commands) {
                cmd := m.commands[idx]
                m.runCommand(cmd.cmd, cmd.prompt)
            }
        } else if m.focus == focusInput {
            switch msg.String() {
            case "enter":
                inputValue := m.input.Value()
                idx := m.list.Index()
                if idx >= 0 && idx < len(m.commands) {
                    cmd := m.commands[idx]
                    fullCmd := append(cmd.cmd, inputValue)
                    m.runCommand(fullCmd, false)
                }
                m.input.SetValue("")
                m.focus = focusList
            case "tab":
                if len(m.completions) > 0 {
                    m.currentIndex = (m.currentIndex + 1) % len(m.completions)
                    m.input.SetValue(m.completions[m.currentIndex])
                }
            }
        } else if m.focus == focusViewport && msg.String() == "/" {
            m.filterOutput()
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

func (m *model) runCommand(cmd []string, prompt bool) {
    if len(cmd) == 0 {
        return
    }

    if prompt {
        m.input.SetValue("")
        m.input.Focus()
        m.focus = focusInput
        return
    }

    m.output += fmt.Sprintf("Running command: %s\n", strings.Join(cmd, " "))
    c := exec.Command(cmd[0], cmd[1:]...)
    var out bytes.Buffer
    c.Stdout = &out
    c.Stderr = &out

    if err := c.Run(); err != nil {
        m.output += fmt.Sprintf("Error: %v\n", err)
    } else {
        m.output += out.String()
    }
    m.viewport.SetContent(m.output)
    m.viewport.GotoBottom()
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
        m.viewport.SetContent(lines[idx])
    }
}

func (m model) View() string {
    var listStyle, viewportStyle, inputStyle lipgloss.Style
    switch m.focus {
    case focusList:
        listStyle = focusedBorder
        viewportStyle = normalBorder
        inputStyle = normalBorder
    case focusViewport:
        listStyle = normalBorder
        viewportStyle = focusedBorder
        inputStyle = normalBorder
    case focusInput:
        listStyle = normalBorder
        viewportStyle = normalBorder
        inputStyle = focusedBorder
    }

    listView := listStyle.Render(m.list.View())
    viewportView := viewportStyle.Render(m.viewport.View())
    inputView := inputStyle.Render(m.input.View())

    helpView := ""
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
        log.Fatalf("Error loading config: %v", err)
    }

    p := tea.NewProgram(initialModel(commands, vpDimensions, listDimensions, tiDimensions, completions))
    if err := p.Start(); err != nil {
        log.Fatalf("Error: %v", err)
    }
}
