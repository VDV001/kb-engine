package main

import (
	"io"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// synonymsFor подключает серверу тот же словарь, которым переводит терминал.
//
// Заглушка: поведение появится следующим коммитом.
func synonymsFor(_ string, _ io.Writer) httpapi.Option {
	return nil
}
