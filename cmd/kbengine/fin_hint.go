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

// finHint печатает подсказку, если отвергнутый флаг живёт у соседа.
func finHint(stderr io.Writer, said, subcommand string) {
	m := rejectedFlagRe.FindStringSubmatch(said)
	if m == nil {
		return
	}
	owners := finFlagOwners(m[1], subcommand)
	if len(owners) == 0 {
		return
	}
	fmt.Fprintf(stderr, "флаг --%s принимают: %s\n", m[1], strings.Join(owners, ", "))
}
