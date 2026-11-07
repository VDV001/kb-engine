package mcpserver

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/domain"
)

// AccessFinder finds access cards (#494); реализует creds.Service. Пароля в
// карточке нет по устройству, а этот адаптер еще и не отдает заметку.
type AccessFinder interface {
	Find(q string) ([]domain.Credential, error)
}

type accessArgs struct {
	Query string `json:"query" jsonschema:"слова: продукт, среда (прод, стенд, тест), логин, роль, адрес; пустой — все доступы"`
}

// accessOut is a card for the agent. Ни пароля, ни заметки: все, что получает
// агент, уходит провайдеру модели, а в заметку пишут что угодно — и пароль.
type accessOut struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	Env      string `json:"env"`
	URL      string `json:"url,omitempty"`
	Host     string `json:"host,omitempty"`
	Login    string `json:"login"`
	Role     string `json:"role,omitempty"`
	Rights   string `json:"rights,omitempty"`
	IssuedBy string `json:"issued_by,omitempty"`
	IssuedAt string `json:"issued_at,omitempty"`
	// Password — не значение, а как человеку взять пароль самому.
	Password string `json:"how_to_get_password"`
}

// addAccessTool registers search_access over a.
func addAccessTool(s *mcp.Server, j journal, a AccessFinder) {
	addTool(s, j, &mcp.Tool{
		Name: "search_access",
		Description: "Доступы владельца к продуктам: продукт, среда (prod, stage, test, dev), адрес, " +
			"хост, логин, роль, права, кто выдал. Паролей здесь нет и не будет: пароль человек " +
			"кладет в буфер сам командой из поля how_to_get_password (Touch ID спросит Мак). " +
			"Не проси пароль и не предлагай вставить его в чат.",
	}, func(in accessArgs) []string {
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(in accessArgs) (map[string]any, error) {
		found, err := a.Find(in.Query)
		if err != nil {
			return nil, err
		}
		out := make([]accessOut, 0, len(found))
		for _, c := range found {
			o := accessOut{ID: c.ID(), Product: c.Product(), Env: string(c.Env()), URL: c.URL(), Host: c.Host(), Login: c.Login(),
				Role: c.Role(), Rights: c.Rights(), IssuedBy: c.IssuedBy(), Password: "kbengine creds copy " + c.ID()}
			if !c.IssuedAt().IsZero() {
				o.IssuedAt = c.IssuedAt().Format(time.DateOnly)
			}
			out = append(out, o)
		}
		return map[string]any{"found": len(out), "access": out}, nil
	})
}
