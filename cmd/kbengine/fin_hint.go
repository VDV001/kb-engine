package main

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Подсказка о том, у какой подкоманды искомый флаг есть.
//
// Повод замеренный: 26 отказов `fin` из 94 прогонов — перенос пары флагов с
// соседней подкоманды. Источников денег два, и принимают их по-разному:
// `--from` (книга) у balance/sync/import, `--ledger` (журнал) у восьми
// подкоманд, кроме balance. Разнобой не случаен — balance пишет на лист
// «Счета», report читает журнал, — но снаружи об этом не сказано нигде.
//
// Движок уже применяет этот приём к ДАННЫМ: незнакомое написание места
// приводится к известному, и подстановка называется вслух. Здесь тот же приём
// применён к собственному интерфейсу.

// rejectedFlagRe достаёт имя флага из сообщения пакета flag. Формат задан
// стандартной библиотекой и меняется вместе с ней.
var rejectedFlagRe = regexp.MustCompile(`flag provided but not defined: -+([a-zA-Z][a-zA-Z0-9-]*)`)

// usageFlagRe достаёт имена флагов из текста помощи.
//
// ponytail: флаги вычитываются из ПОМОЩИ, а не спрашиваются у FlagSet — тот
// строится внутри каждой подкоманды и снаружи недоступен. Потолок: разбор
// зависит от формата, который печатает flag.PrintDefaults. Путь наверх —
// вынести объявление флагов каждой подкоманды в отдельную функцию
// `newXxxFlags() *flag.FlagSet` и спрашивать множество прямо у неё; это
// девять правок и отдельная работа. Пока формат стережёт тест: он требует,
// чтобы у `--from` нашлись ровно balance, import и sync.
var usageFlagRe = regexp.MustCompile(`(?m)^\s+-([a-zA-Z][a-zA-Z0-9-]*)`)

// headWriter пропускает поток дальше и запоминает его начало.
//
// Начало, а не весь вывод: сообщение об отказе разбора flag печатает первым, и
// держать в памяти всё, что скажет команда, незачем.
type headWriter struct {
	w    io.Writer
	head bytes.Buffer
}

const headLimit = 4 << 10

func (h *headWriter) Write(p []byte) (int, error) {
	if room := headLimit - h.head.Len(); room > 0 {
		h.head.Write(p[:min(room, len(p))])
	}
	return h.w.Write(p)
}

// finFlagOwners называет подкоманды fin, у которых такой флаг есть.
//
// Множество берётся из реестра finCommands и живой помощи каждой подкоманды, а
// не из вписанного списка: список разошёлся бы с кодом при первом новом флаге,
// и подсказка стала бы врать — хуже, чем молчать.
func finFlagOwners(flagName, except string) []string {
	var owners []string
	for name := range finCommands {
		if name == except {
			continue
		}
		if slices.Contains(finFlagsOf(name), flagName) {
			owners = append(owners, name)
		}
	}
	slices.Sort(owners)
	return owners
}

// finFlagsOf спрашивает подкоманду, какие флаги она принимает.
func finFlagsOf(name string) []string {
	sub, ok := finCommands[name]
	if !ok {
		return nil
	}
	var help bytes.Buffer
	sub([]string{"--help"}, strings.NewReader(""), &help, &help)

	var flags []string
	for _, m := range usageFlagRe.FindAllStringSubmatch(help.String(), -1) {
		flags = append(flags, m[1])
	}
	return flags
}

// finHint печатает подсказку об отвергнутом флаге: либо он живёт у соседней
// подкоманды, либо он в одной правке от настоящего. Оба ответа берутся из
// живых FlagSet'ов, третьего («не знаю, посмотри usage») здесь не нужно —
// usage уже напечатан пакетом flag строкой выше.
func finHint(stderr io.Writer, said, subcommand string) {
	m := rejectedFlagRe.FindStringSubmatch(said)
	if m == nil {
		return
	}
	rejected := m[1]

	if owners := finFlagOwners(rejected, subcommand); len(owners) > 0 {
		fmt.Fprintf(stderr, "флаг --%s принимают: %s\n", rejected, strings.Join(owners, ", "))
		return
	}
	if near, owners := finNearMiss(rejected); near != "" {
		fmt.Fprintf(stderr, "похоже на --%s (принимают: %s)\n", near, strings.Join(owners, ", "))
	}
}

// finNearMiss ищет флаг, отличающийся ровно ОДНОЙ правкой.
//
// Все три условия выведены замером по настоящим флагам fin, а не выбраны на
// глаз:
//
//   - ровно одна правка. На двух правках ближайшим к `--book` оказывается
//     `--bank` — это СЧЁТ, тогда как книга здесь `--from`. Подсказать не то
//     поле в денежном инструменте хуже, чем промолчать.
//   - имя от четырёх букв. На коротком имени одна правка — уже другое слово:
//     `--last` отстоит от `--cat` на две, `--list` от `--init` на три, и обе
//     подсказки были бы шумом.
//   - ровно один кандидат. Двусмысленная подсказка хуже молчания; среди самих
//     флагов пар на расстоянии одной правки нет (ближайшие — `account` и
//     `amount` на двух), так что условие стережёт будущее, а не настоящее.
func finNearMiss(flagName string) (string, []string) {
	if len([]rune(flagName)) < 4 {
		return "", nil
	}
	var found string
	for name := range finCommands {
		for _, f := range finFlagsOf(name) {
			if f == found || editDistance(flagName, f) != 1 {
				continue
			}
			if found != "" {
				return "", nil // кандидатов больше одного — молчим
			}
			found = f
		}
	}
	if found == "" {
		return "", nil
	}
	return found, finFlagOwners(found, "")
}

// editDistance — расстояние Левенштейна, две строки рун.
//
// ponytail: своя реализация вместо зависимости. Потолок — наивные O(n·m) и
// отсутствие транспозиции (перестановка соседних букв стоит две правки, а не
// одну), поэтому `--acocunt` подсказки не получит. Имена флагов короче
// пятнадцати символов, вызывается только на пути отказа; путь наверх —
// расстояние Дамерау-Левенштейна, если такие опечатки появятся в журнале.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
