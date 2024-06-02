function read_directory(dir)
    local i, t, popen = 0, {}, io.popen
    local pfile = popen('ls -a "'..dir..'"')
    for filename in pfile:lines() do
        i = i + 1
        t[i] = filename
    end
    pfile:close()
    return t
end

completions = read_directory(".")

return {
    buttons = {
        { name = "Echo Hey", cmd = {"echo", "hey"}, prompt = false },
        { name = "List Directory", cmd = {"ls", "-l"}, prompt = false },
        { name = "Print Working Directory", cmd = {"pwd"}, prompt = false },
        { name = "Date", cmd = {"date"}, prompt = false },
        { name = "python test", cmd = {"python3", "test.py"}, prompt = true },
    },
    viewport = {
        width = 110,
        height = 20
    },
    list = {
        width = 45,
        height = 20
    },
    textinput = {
        width = 110-3,
    },
    completions = completions
}
