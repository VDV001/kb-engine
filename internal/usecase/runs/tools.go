package runs

// Заготовка под счётчик вызовов MCP: символы объявлены, поведения нет —
// следующий коммит наполняет их, тесты рядом сейчас красные.

// ToolCommand — имя вызова инструмента в журнале прогонов.
func ToolCommand(tool string) string { return tool }

// ToolOf разбирает имя вызова обратно в имя инструмента.
func ToolOf(command string) (string, bool) { return "", false }
