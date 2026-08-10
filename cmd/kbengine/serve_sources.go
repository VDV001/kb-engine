package main

import (
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

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
// serveSources — единственный список необязательных источников на всю команду.
// Из него растут обе формы ответа: строка в логе запуска и поле в /api/engine.
// Держать два списка значило бы однажды научить лог одному, а страницу другому.
//
// Порядок — как у флагов в объявлении команды: отчёт читают рядом со своей же
// строкой запуска, и алфавит там мешал бы, а не помогал.
func serveSources(configPath, ledgerPath, workbookPath, changelogPath, nowPath, teamPath, projectsPath, mediaPath string, mapPaths []string) []source {
	return []source{
		{flag: "analytics-config", path: configPath},
		{flag: "ledger", path: ledgerPath},
		{flag: "from", path: workbookPath},
		{flag: "changelog", path: changelogPath},
		{flag: "now", path: nowPath},
		{flag: "team", path: teamPath},
		{flag: "projects", path: projectsPath},
		{flag: "media", path: mediaPath},
		// Карт бывает несколько, а источник один: подключён он тогда, когда
		// передали хоть одну. Пути отсюда наружу не уезжают — на страницу
		// едет только имя флага.
		{flag: "maps", path: strings.Join(mapPaths, ", ")},
	}
}

// sourceStatuses переводит источники в то, что уезжает на страницу: имя флага и
// факт подключения, без путей. Пути остаются в терминале — дашборд бывает виден
// не только тому, кто его запускал, а путь к файлу владельца это его данные.
func sourceStatuses(srcs []source) []httpapi.SourceStatus {
	out := make([]httpapi.SourceStatus, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, httpapi.SourceStatus{Flag: s.flag, Connected: s.path != ""})
	}
	return out
}

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
