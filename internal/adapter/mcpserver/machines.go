package mcpserver

import (
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/domain"
)

// MachineFinder finds machine cards (#549); реализует machines.Service. Пароля
// в карточке нет по устройству, а выдача агенту (domain.Machine.AgentView) не
// содержит ни пути ключа, ни подсказки про токен, ни заметки.
type MachineFinder interface {
	Find(q string) ([]domain.Machine, error)
}

// errMachinesUnreadable — карточки не прочитались. Своя ошибка вместо настоящей:
// в той — полный путь (имя пользователя) и кусок файла, а все, что получает
// агент, уходит провайдеру модели.
var errMachinesUnreadable = errors.New("карточки машин не прочитать; подробности в окне DVK или в терминале")

type machineArgs struct {
	Query string `json:"query" jsonschema:"слова: имя машины, группа, хост, пользователь, проект или путь на машине; пустой — все машины"`
}

// addMachineTool registers search_machines over f.
func addMachineTool(s *mcp.Server, j journal, f MachineFinder) {
	addTool(s, j, &mcp.Tool{
		Name: "search_machines",
		Description: "Машины владельца (серверы по ssh), на которых лежат проекты: имя, группа, хост, порт, " +
			"пользователь, способ входа (password, key, token), через какую машину заходить и что на ней " +
			"лежит (проект, путь, порт). Паролей, путей ключей и заметок здесь нет и не будет: вход человек " +
			"делает сам. Не проси пароль и не предлагай вставить его в чат.",
	}, func(in machineArgs) []string {
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(in machineArgs) (map[string]any, error) {
		found, err := f.Find(in.Query)
		if err != nil {
			return nil, errMachinesUnreadable
		}
		out := make([]domain.MachineAgentView, 0, len(found))
		for _, m := range found {
			out = append(out, m.AgentView())
		}
		return map[string]any{"found": len(out), "machines": out}, nil
	})
}
