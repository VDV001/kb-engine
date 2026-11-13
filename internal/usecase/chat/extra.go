package chat

import "context"

// Extra — инструменты модуля окна в режиме «модель ищет» («Маркеты», #580):
// модуль сам описывает их модели и сам исполняет; чат только передает.
type Extra interface {
	// Prompt — правило модуля, дописывается к системному.
	Prompt() string
	// Tools — описания инструментов модуля.
	Tools() []Tool
	// Exec runs a tool of Tools; ok false — инструмент не этого модуля.
	Exec(ctx context.Context, name string, args []byte) (out string, ok bool)
}
