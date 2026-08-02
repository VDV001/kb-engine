package main

import "strings"

// source — внешний файл, который движку могли передать флагом. Пустой path
// означает «флаг не передавали», и это не ошибка: без семантического слоя,
// журнала или команды дашборд работает, просто читающие их вкладки пусты.
type source struct {
	flag string
	path string
}

// startupSources собирает отчёт о том, что подключено, а что нет.
//
// Вторая строка — вся суть: без неё запуск без источника и запуск с ним
// выглядели одинаково, и пустая вкладка читалась как поломка движка, а не как
// невыполненная просьба загрузить файл. Это то же Правило 11, по которому
// раньше чинили --from, --team, --projects и --changelog; --analytics-config
// оставался последним, кто молчал.
//
// Порядок сохраняется в том виде, в каком источники объявлены в команде: он
// повторяет порядок флагов в справке, и читать отчёт рядом со своей же
// строкой запуска проще, чем сверяться с алфавитом.
func startupSources(srcs []source) []string {
	var connected, missing []string
	for _, s := range srcs {
		if s.path != "" {
			connected = append(connected, "--"+s.flag)
			continue
		}
		missing = append(missing, "--"+s.flag)
	}
	var lines []string
	if len(connected) > 0 {
		lines = append(lines, "kbengine: sources connected: "+strings.Join(connected, ", "))
	}
	if len(missing) > 0 {
		lines = append(lines, "kbengine: sources not connected: "+strings.Join(missing, ", ")+
			" — the views that read them stay empty")
	}
	return lines
}
