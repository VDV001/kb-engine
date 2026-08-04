package catalogjson

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/botinbox"
)

// HabrIDFill — одна запись, которой номер статьи проставляется из её адреса.
type HabrIDFill struct {
	EntryID int
	Title   string
	HabrID  int
}

// HabrIDConflict — у записи есть и номер, и адрес, и они говорят разное.
type HabrIDConflict struct {
	EntryID int
	Title   string
	Stored  int
	InURL   int
}

// HabrIDPlan — что миграция сделает и чего делать не станет.
type HabrIDPlan struct {
	Filled []HabrIDFill
	// Normalized — номер хранился строкой и приводится к числу. Поле держало
	// оба типа сразу (на живом каталоге 281 число против 225 строк), и пока это
	// так, сравнение с числом промахивается на половине записей — а промах
	// выглядит как «такой статьи в базе нет».
	Normalized []HabrIDFill
	Conflicts  []HabrIDConflict
}

// readHabrID разбирает поле, которое хранит номер и числом, и строкой.
//
// Второе значение говорит, лежала ли там строка: строка — это то же знание в
// форме, в которой оно не сравнивается с числом, поэтому её приводят к числу.
func readHabrID(raw json.RawMessage) (id int, wasString, present bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, false, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return v, true, true
		}
	}
	return 0, false, false
}

// MigrateHabrIDs проставляет номер статьи тем записям, у которых он известен из
// адреса и не записан в поле.
//
// Повод измерен: номер виден в адресе у 1266 записей каталога, а поле заполнено
// у 506 — среди пришедших из бот-инбокса у 36 из 717. Пока это так, проверка
// «нет ли такой статьи в базе» по этому полю смотрит на часть базы и молчит про
// остальную, а молчание читается как «дубликатов нет».
//
// Расхождение между полем и адресом не решается: движок не знает, что из двух
// верно, и молча выбрав одно, он стёр бы решение человека. Такие записи только
// называются.
func MigrateHabrIDs(path string, apply bool) (HabrIDPlan, error) {
	members, entries, err := readEntries(path)
	if err != nil {
		return HabrIDPlan{}, err
	}

	var plan HabrIDPlan
	for i, raw := range entries {
		edited, changed, err := fillHabrID(raw, &plan)
		if err != nil {
			return HabrIDPlan{}, err
		}
		if changed {
			entries[i] = edited
		}
	}

	if !apply || (len(plan.Filled) == 0 && len(plan.Normalized) == 0) {
		return plan, nil
	}
	doc, err := assemble(members, entries)
	if err != nil {
		return plan, err
	}
	if err := writeFileAtomic(path, doc); err != nil {
		return plan, err
	}
	return plan, nil
}

// fillHabrID решает судьбу одной записи и возвращает её обновлённой, когда
// номер надо записать. Вынесено из цикла: решений здесь пять, и вместе с
// обходом каталога они читались как одно длинное.
func fillHabrID(raw json.RawMessage, plan *HabrIDPlan) (json.RawMessage, bool, error) {
	var head struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		URL   string `json:"url"`
		// Сырым: поле хранит номер то числом, то строкой, и разбор в int
		// падает на половине живого каталога.
		HabrID json.RawMessage `json:"habr_id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, false, fmt.Errorf("parse entry: %w", err)
	}
	inURL := botinbox.HabrIDFromURL(head.URL)
	if inURL == 0 {
		return nil, false, nil
	}

	stored, wasString, present := readHabrID(head.HabrID)
	switch {
	case present && stored != inURL:
		// Что верно — поле или адрес — движок не знает, и молча выбрав одно,
		// он стёр бы решение человека.
		plan.Conflicts = append(plan.Conflicts, HabrIDConflict{
			EntryID: head.ID, Title: head.Title, Stored: stored, InURL: inURL,
		})
		return nil, false, nil
	case present && !wasString:
		return nil, false, nil // уже число и уже верно — трогать нечего
	case present:
		plan.Normalized = append(plan.Normalized, HabrIDFill{EntryID: head.ID, Title: head.Title, HabrID: inURL})
	default:
		plan.Filled = append(plan.Filled, HabrIDFill{EntryID: head.ID, Title: head.Title, HabrID: inURL})
	}

	encoded, err := marshalNoEscape(inURL)
	if err != nil {
		return nil, false, err
	}
	edited, err := readTopLevel(raw)
	if err != nil {
		return nil, false, err
	}
	obj, err := assembleObject(setMember(edited, "habr_id", encoded))
	if err != nil {
		return nil, false, err
	}
	return obj, true, nil
}
