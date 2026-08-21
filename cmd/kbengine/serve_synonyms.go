package main

import (
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// synonymsFor подключает серверу тот же словарь, которым переводит терминал.
//
// До неё словарь читал только tui: правило существовало, а веб позвать его не
// мог — тот же класс, что #252, только не в коде поиска, а в проводке.
//
// Отсутствие файла не ошибка и не повод не запуститься: поиск продолжает
// работать подстрокой, транслитерацией и с опечатками. Но сказать об этом
// обязательно — «перевода нет, потому что файла нет» и «перевод не сработал»
// снаружи выглядят одинаково, а лечатся по-разному.
func synonymsFor(catalogPath string, stderr io.Writer) httpapi.Option {
	path := searchsyn.PathNextTo(catalogPath)
	syn, err := searchsyn.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v — поиск ищет подстрокой, транслитерацией и с опечатками, но не переводит термины\n", err)
		return nil
	}
	return httpapi.WithSynonyms(search.New(syn))
}
