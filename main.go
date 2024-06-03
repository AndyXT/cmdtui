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
    help "github.com/charmbracelet/bubbles/help"
    key "github.com/charmbracelet/bubbles/key"
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
    tabBorder      = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
    activeTabBorder = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).BorderForeground(lipgloss.Color("205"))
    tab            = lipgloss.NewStyle().Padding(0, 1)
    activeTab      = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("205")).Bold(true)
    tabGap         = tab.Copy().Padding(0, 2)
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
    viewports      []viewport.Model // Change to slice of viewports
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
    help           help.Model
    keys           keyMap
    prompInput     bool
    currentTab     int // Current tab index
    tabs           []string // Tabs titles
}

// Add a function to initialize tabs
func initTabs() []string {
    return []string{"Main", "Tab 2", "Tab 3"} // Add more tabs as needed
}

type keyMap struct {
    NextFocus key.Binding
    PrevFocus key.Binding
    Quit      key.Binding
    Help      key.Binding
    Execute   key.Binding
    Filter    key.Binding
    Refresh   key.Binding
    NextTab   key.Binding // Key binding for switching to the next tab
    PrevTab   key.Binding // Key binding for switching to the previous tab
}

var keys = keyMap{
    NextFocus: key.NewBinding(
        key.WithKeys("ctrl+n"),
        key.WithHelp("ctrl+n", "next focus"),
    ),
    PrevFocus: key.NewBinding(
        key.WithKeys("ctrl+p"),
        key.WithHelp("ctrl+p", "prev focus"),
    ),
    Quit: key.NewBinding(
        key.WithKeys("q"),
        key.WithHelp("q", "quit"),
    ),
    Help: key.NewBinding(
        key.WithKeys("?"),
        key.WithHelp("?", "toggle help"),
    ),
    Execute: key.NewBinding(
        key.WithKeys("enter"),
        key.WithHelp("enter", "execute command"),
    ),
    Filter: key.NewBinding(
        key.WithKeys("/"),
        key.WithHelp("/", "filter output"),
    ),
    Refresh: key.NewBinding(
        key.WithKeys("ctrl+l"),
        key.WithHelp("ctrl+l", "refresh"),
    ),
    NextTab: key.NewBinding(
        key.WithKeys("]"),
        key.WithHelp("]", "next tab"),
    ),
    PrevTab: key.NewBinding(
        key.WithKeys("["),
        key.WithHelp("[", "previous tab"),
    ),
}

func (k keyMap) ShortHelp() []key.Binding {
    return []key.Binding{k.NextFocus, k.PrevFocus, k.Execute, k.Filter, k.Refresh, k.Help, k.Quit, k.NextTab, k.PrevTab}
}

func (k keyMap) FullHelp() [][]key.Binding {
    return [][]key.Binding{
        {k.NextFocus, k.PrevFocus, k.Execute, k.Filter},
        {k.Refresh, k.Help, k.Quit},
        {k.NextTab, k.PrevTab},
    }
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
    l.SetFilteringEnabled(true)
    l.SetShowHelp(false)

    mainViewport := viewport.New(vpDimensions.width, vpDimensions.height-tiDimensions.height-4)
    mainViewport.SetContent("Output will be displayed here...")
    mainViewport.MouseWheelEnabled = true

    otherViewport := viewport.New(vpDimensions.width, vpDimensions.height-tiDimensions.height-4)
    otherViewport.SetContent("")

    vp := []viewport.Model{mainViewport, otherViewport, otherViewport} // Add more viewports as needed

    ti := textinput.New()
    ti.Placeholder = "Type a command..."
    ti.Focus()
    ti.Width = tiDimensions.width

    h := help.New()
    k := keys

    return model{
        list:           l,
        viewports:      vp,
        input:          ti,
        focus:          focusList,
        commands:       commands,
        showHelp:       true,
        vpDimensions:   vpDimensions,
        listDimensions: listDimensions,
        tiDimensions:   tiDimensions,
        completions:    completions,
        currentIndex:   -1,
        help:           h,
        keys:           k,
        prompInput:     false,
        currentTab:     0,
        tabs:           initTabs(),
    }
}

func (m model) Init() tea.Cmd {
    return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch {
        case key.Matches(msg, m.keys.NextFocus):
            m.focus = (m.focus + 1) % 3
        case key.Matches(msg, m.keys.PrevFocus):
            m.focus = (m.focus + 2) % 3
        case key.Matches(msg, m.keys.Quit):
            return m, tea.Quit
        case key.Matches(msg, m.keys.Help):
            m.showHelp = !m.showHelp
        case key.Matches(msg, m.keys.Refresh):
            if m.focus == focusViewport {
                m.viewports[m.currentTab].SetContent(m.output)
                m.viewports[m.currentTab].GotoBottom()
            }
        case key.Matches(msg, m.keys.NextTab):
            m.currentTab = (m.currentTab + 1) % len(m.viewports)
        case key.Matches(msg, m.keys.PrevTab):
            m.currentTab = (m.currentTab - 1 + len(m.viewports)) % len(m.viewports)
        }

        if m.focus == focusList && key.Matches(msg, m.keys.Execute) {
            idx := m.list.Index()
            if idx >= 0 && idx < len(m.commands) {
                cmd := m.commands[idx]
                if cmd.prompt {
                    // Command requires input, prompt the user
                    m.input.SetValue("")
                    m.input.Focus()
                    m.focus = focusInput
                    m.prompInput = true
                    m.currentIndex = idx
                    return m, nil
                }
                m.runCommand(cmd)
            }
        } else if m.focus == focusInput {
            switch msg.String() {
            case "enter":
                inputValue := m.input.Value()
                if inputValue != "" {
                    if m.prompInput == true {
                        // Get cmd from list to append to it
                        idx := m.currentIndex
                        if idx >= 0 && idx < len(m.commands) {
                            cmd := m.commands[idx]
                            fullCmd := append(cmd.cmd, inputValue)
                            fullCommand := command{
                                name:   cmd.name,
                                cmd:    fullCmd,
                                prompt: false,
                            }
                            m.runCommand(fullCommand)
                        }
                        m.prompInput = false
                    } else {
                        // Create command structure for arbitrary command
                        cmd := command{
                            name:   inputValue,
                            cmd:    strings.Fields(inputValue),
                            prompt: false,
                        }
                        m.runCommand(cmd)
                    }
                }
                m.input.SetValue("")
                m.focus = focusList
            case "tab":
                if len(m.completions) > 0 {
                    m.currentIndex = (m.currentIndex + 1) % len(m.completions)
                    m.input.SetValue(m.completions[m.currentIndex])
                }
            }
        } else if m.focus == focusViewport && key.Matches(msg, m.keys.Filter) {
            m.filterOutput()
        }
    case tea.MouseMsg:
        switch msg.Type {
        case tea.MouseLeft:
            if m.focus == focusList {
                // If clicking on the list, change focus to the viewport
                m.focus = focusViewport
            } else if m.focus == focusViewport {
                // If clicking on the viewport, change focus to the input
                m.focus = focusInput
                m.input.Focus()
            } else if m.focus == focusInput {
                // If clicking on the input, change focus to the list
                m.focus = focusList
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
        m.viewports[m.currentTab], viewportCmd = m.viewports[m.currentTab].Update(msg)
        cmds = append(cmds, viewportCmd)
    }

    return m, tea.Batch(cmds...)
}

func (m *model) runCommand(cmd command) {
    if len(cmd.cmd) == 0 {
        return
    }

    if cmd.prompt {
        m.input.SetValue("")
        m.input.Focus()
        m.focus = focusInput
        return
    }

    m.output += fmt.Sprintf("Running command: %s\n", strings.Join(cmd.cmd, " "))
    c := exec.Command(cmd.cmd[0], cmd.cmd[1:]...)
    var out bytes.Buffer
    c.Stdout = &out
    c.Stderr = &out

    if err := c.Run(); err != nil {
        m.output += fmt.Sprintf("Error: %v\n", err)
    } else {
        m.output += out.String()
    }
    m.viewports[m.currentTab].SetContent(m.output)
    m.viewports[m.currentTab].GotoBottom()

    // Reset input and focus after running a command
    m.input.SetValue("")
    m.focus = focusList
    m.prompInput = false // Reset the prompt input flag
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
        m.viewports[m.currentTab].SetContent(lines[idx])
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

    // Render tabs
    var tabViews []string
    for i, t := range m.tabs {
        var style lipgloss.Style
        if i == m.currentTab {
            style = activeTab
        } else {
            style = tab
        }
        tabViews = append(tabViews, style.Render(t))
    }

    tabs := lipgloss.JoinHorizontal(lipgloss.Top, tabGap.Render("|"), lipgloss.JoinHorizontal(lipgloss.Top, tabViews...))

    listView := listStyle.Render(m.list.View())
    viewportView := viewportStyle.Render(m.viewports[m.currentTab].View())
    inputView := inputStyle.Render(m.input.View())

    helpView := ""
    if m.showHelp {
        helpView = "\n\n" + m.help.View(m.keys)
    }

    return docStyle.Render(
        lipgloss.JoinVertical(
            lipgloss.Left,
            tabs,
            lipgloss.JoinHorizontal(
                lipgloss.Top,
                listView,
                lipgloss.JoinVertical(
                    lipgloss.Left,
                    viewportView,
                    inputView,
                ),
            ),
        ),
    ) + helpView
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

    p := tea.NewProgram(
        initialModel(commands, vpDimensions, listDimensions, tiDimensions, completions),
        tea.WithAltScreen(),      // Use alternate screen buffer
        tea.WithMouseCellMotion(), // Enable mouse support
    )
    if err := p.Start(); err != nil {
        log.Fatalf("Error: %v", err)
    }
}
