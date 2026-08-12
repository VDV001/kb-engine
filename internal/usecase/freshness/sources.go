package freshness

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Source — один подключённый источник страницы: чем его правят и когда правили.
//
// Facts приходят снаружи, потому что опоры у страниц разные: у Now это записи
// каталога и операции, у Projects — версия, которую страница называет о самом
// движке. Собирать их здесь значило бы затащить в usecase знание обо всех
// источниках разом.
type Source struct {
	Name     string
	Flag     string
	Now      time.Time
	EditedAt time.Time
	// Anchored — у этого источника вообще есть с чем сверяться. Отдельно от
	// наличия находок: «опор нет» и «опоры есть, всё сошлось» — разные ответы,
	// и путать их значит показывать проверенным то, что не проверялось.
	Anchored bool
	Facts    []Fact
}

// SourceState — что известно про свежесть одного источника.
type SourceState struct {
	Name     string
	Flag     string
	EditedAt time.Time
	Behind   bool
	// Unknown — дату правки прочитать не удалось.
	Unknown bool
	// NoAnchors — сверять не с чем: у этого источника нет опор в базе, и
	// возраст сам по себе приговором не является. Признак отдельный, потому
	// что зелёная галочка здесь означала бы «проверено», а проверки не было.
	NoAnchors bool
	// StaleBuild — страница называет версию новее собранной: отстала не она, а
	// движок. Признак отдельный от Behind, потому что лечится другим — не
	// правкой файла, а обновлением сборки.
	StaleBuild bool
	// AgeDays — сколько дней прошло с правки. Факт, даже когда он не приговор:
	// человеку полезно видеть, что страницу не трогали полгода, даже если
	// сверить её не с чем.
	AgeDays int
	Facts   []Fact
}

// CheckSource приводит один источник к общему виду.
func CheckSource(s Source) SourceState {
	out := SourceState{Name: s.Name, Flag: s.Flag, EditedAt: s.EditedAt, Facts: s.Facts}
	if s.EditedAt.IsZero() {
		out.Unknown = true
		return out
	}
	if !s.Now.IsZero() {
		out.AgeDays = int(s.Now.Sub(s.EditedAt).Hours() / 24)
	}
	for _, f := range s.Facts {
		if f.Kind == KindStaleBuild {
			out.StaleBuild = true
			continue
		}
		out.Behind = true
	}
	out.NoAnchors = !s.Anchored
	return out
}

// versionPattern — версия в том виде, в каком её пишут в тексте страницы.
var versionPattern = regexp.MustCompile(`v(\d+\.\d+\.\d+)`)

// releaseVersion — чистый семвер и ничего больше. Сборка из исходников даёт
// псевдоверсию вида 0.15.1-0.20260804145247-fea441f0ae51+dirty, и сравнивать с
// ней нельзя: на экран уехало бы «сейчас v0.15.1-0.2026…+dirty», что не
// является ответом на вопрос, какая версия сейчас.
var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// pseudoVersion — версия сборки из исходников, как её строит Go:
// 0.15.1-0.20260804151919-e9b33a0aa068, иногда с суффиксом +dirty.
var pseudoVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)-0\.\d{14}-[0-9a-f]+`)

// BaseRelease возвращает последний выпуск, о котором версия говорит, или "",
// когда она не говорит ни о чём.
//
// Псевдоверсия — не «не знаю». Go строит её как следующий patch после
// последнего тега, поэтому 0.15.1-0.… означает «собрано после 0.15.0». Разница
// принципиальная: `kbup` собирает движок из исходников, и пока такая версия
// считалась неизвестной, проверка Projects не срабатывала никогда именно в том
// сценарии, ради которого заводилась.
func BaseRelease(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if releaseVersion.MatchString(v) {
		return v
	}
	m := pseudoVersion.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil || patch == 0 {
		return ""
	}
	return fmt.Sprintf("%s.%s.%d", m[1], m[2], patch-1)
}

// IsReleaseVersion сообщает, знает ли движок о себе версию, с которой можно
// сверяться, — выпуск или сборку после известного выпуска.
func IsReleaseVersion(v string) bool {
	return BaseRelease(v) != ""
}

// Виды находок о версии. Различать их обязан движок: «отстала страница» лечится
// правкой файла, «отстала сборка» — обновлением движка, и жёлтый ярлык на обоих
// послал бы человека править то, что верно.
const (
	KindVersionMention = "version-mention"
	KindStaleBuild     = "stale-build"
)

// newer сообщает, что выпуск a вышел позже b. Оба уже приведены к чистому
// семверу, поэтому хватает сравнения трёх чисел — тянуть ради этого
// golang.org/x/mod в проект с четырьмя прямыми зависимостями незачем.
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != 3 || len(pb) != 3 {
		return false
	}
	for i := range 3 {
		x, errA := strconv.Atoi(pa[i])
		y, errB := strconv.Atoi(pb[i])
		if errA != nil || errB != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// VersionMention ищет строку, где страница называет версию продукта, и
// сравнивает её с настоящей.
//
// Опора живая: карточка kb-engine на странице Projects говорила v0.5.0, когда
// движок отвечал 0.15.0 — страница врала о собственном проекте втрое, и заметить
// это можно было только вручную.
//
// Проверяются строковые значения целиком, а не весь файл разом: версия и имя
// должны стоять рядом, иначе «v0.113.0» соседнего проекта прочиталось бы как
// версия этого. Своя версия неизвестна (сборка из исходников даёт псевдоверсию)
// — молчим: судить, не зная о себе правды, хуже, чем не судить.
func VersionMention(text, product, current string) *Fact {
	current = BaseRelease(current)
	if product == "" || current == "" {
		return nil
	}
	// JSON разбирается как JSON. Разрез по `","` — свойство форматирования, а
	// не данных: вынести версию в своё поле рядом с именем естественно, и
	// проверка от этого замолкала, продолжая выглядеть работающей.
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err == nil {
		v := versionNearProduct(doc, product)
		if v == "" {
			return nil
		}
		return versionFact(product, v, current)
	}
	// Markdown иначе не прочитать — здесь разбор по строкам остаётся
	// сознательным решением, а не забытой веткой.
	for line := range strings.SplitSeq(text, "\n") {
		for chunk := range strings.SplitSeq(line, `","`) {
			if !strings.Contains(strings.ToLower(chunk), strings.ToLower(product)) {
				continue
			}
			m := versionPattern.FindStringSubmatch(chunk)
			if m == nil {
				continue
			}
			return versionFact(product, m[1], current)
		}
	}
	return nil
}

// versionFact сравнивает версию, названную страницей, с настоящей. Одно место
// на оба разбора: две копии сравнения однажды разошлись бы в вердикте.
func versionFact(product, mentioned, current string) *Fact {
	if mentioned == current {
		return nil
	}
	// Страница называет версию новее собранной — отстала не она. Случай живой:
	// тег ставится через API, локальная копия о нём не знает, и сборка сразу
	// после выпуска называет предыдущий тег.
	if newer(mentioned, current) {
		return &Fact{
			Kind: KindStaleBuild,
			Text: fmt.Sprintf("страница называет %s v%s, сейчас %s — отстала сборка, а не страница", product, mentioned, current),
		}
	}
	return &Fact{
		Kind: KindVersionMention,
		Text: fmt.Sprintf("страница называет %s v%s, сейчас %s", product, mentioned, current),
	}
}

// versionNearProduct ищет версию продукта в разобранном документе.
//
// Единица поиска — ОБЪЕКТ: имя и версия должны стоять в соседних полях одного
// объекта либо внутри одного значения. Иначе версия соседнего проекта в том же
// массиве прочиталась бы как наша — это не гипотеза, прежний разбор по тексту
// именно так и ошибался.
//
// ponytail: вложенные объекты своей версии продукту не отдают
// (`{"name":"kb-engine","meta":{"version":"v0.5.0"}}` не находится). Живой файл
// такой формы не имеет; если появится — поднимать версию вверх по дереву при
// обходе, а не расширять понятие соседства.
func versionNearProduct(node any, product string) string {
	switch v := node.(type) {
	case map[string]any:
		var strs []string
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if s, ok := v[k].(string); ok {
				strs = append(strs, s)
			}
		}
		if slices.ContainsFunc(strs, func(s string) bool {
			return strings.Contains(strings.ToLower(s), strings.ToLower(product))
		}) {
			for _, s := range strs {
				if m := versionPattern.FindStringSubmatch(s); m != nil {
					return m[1]
				}
			}
		}
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if got := versionNearProduct(v[k], product); got != "" {
				return got
			}
		}
	case []any:
		for _, item := range v {
			if got := versionNearProduct(item, product); got != "" {
				return got
			}
		}
	}
	return ""
}
