package chat

import "context"

// Extra — инструменты модуля окна в режиме «модель ищет» («Маркеты», #580):
// модуль сам описывает их модели и сам исполняет; чат только передает.
type Extra interface {
	// Prompt — правило модуля, дописывается к системному.
	Prompt() string
	// Tools — описания инструментов модуля.
	Tools() []Tool
	// Describe names where tool name goes and what it asks: подключение
	// словами человека; ok false — инструмент не этого модуля.
	Describe(name string, args []byte) (u Use, ok bool)
	// Exec runs a tool of Tools; ok false — инструмент не этого модуля.
	Exec(ctx context.Context, name string, args []byte) (out string, ok bool)
}

// UseKind — куда ходит ход за данными.
type UseKind string

const (
	// UseDocs — источники окна: база знаний, папки, kb-серверы.
	UseDocs UseKind = "docs"
	// UseConnection — подключение окна (кабинет Ozon, MPStats): внешний
	// сервис по ключу человека.
	UseConnection UseKind = "connection"
	// UseWeb — ресурс вне настроенного: ссылка из вопроса.
	UseWeb UseKind = "web"
)

// Use is one place the turn took data from: вид, id подключения или
// источника, имя для человека и что спросили.
type Use struct {
	Kind UseKind `json:"kind"`
	ID   string  `json:"id,omitempty"`
	Name string  `json:"name,omitempty"`
	What string  `json:"what,omitempty"`
}
