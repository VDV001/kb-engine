package finance

// Correction описывает одну подстановку: какое поле, что набрали, что записано.
type Correction struct {
	Field string
	Typed string
	Used  string
}

// CanonicalWith приводит значения записи к написанию, которое в базе уже есть,
// и возвращает список подстановок; словарь при этом решает раньше частоты.
//
// Повод измеренный, а не гипотетический: категория, набранная строчными, дала в
// отчете вторую строку рядом с той же категорией на 236 записей; в живом
// леджере тем же образом разошлись «Своя Компания» и «Пятерочка». Разбивка
// раздваивается молча, и заметить это можно только глазом на витрине.
//
// Что считается тем же значением, решает NormalizeWord — та же функция, по
// которой узнает слова быстрый ввод. Второе определение «одинаковости» тут
// однажды разошлось бы с первым, и разошлось бы незаметно.
//
// Совсем новое значение не трогается: его не с чем сверять, и новая категория —
// законное намерение, а не опечатка.
//
// Порядок «словарь раньше частоты» именно такой, и он проверен живым случаем: в леджере «Пятерочка»
// стоит семь раз против одной «Пятерочки», а владелец 02.08 решил писать
// «Пятерочка» и записал это в словарь. Историческое большинство — след старых
// ошибок, словарь — решение; побеждать должно решение.
func CanonicalWith(existing []Record, voc Vocabulary, p AddParams) (AddParams, []Correction) {
	known := spellings(existing)
	preferred := fromVocabulary(voc)
	var fixed []Correction

	for _, f := range []struct {
		name  string
		field *string
	}{
		{"категория", &p.Category},
		{"подкатегория", &p.Subcategory},
		{"место", &p.Place},
		{"счет", &p.Account},
		{"источник", &p.Source},
	} {
		typed := *f.field
		if typed == "" {
			continue
		}
		used := preferred[f.name+"\x00"+NormalizeWord(typed)]
		if used == "" {
			used = known.canonical(f.name, typed)
		}
		if used == typed {
			continue
		}
		*f.field = used
		fixed = append(fixed, Correction{Field: f.name, Typed: typed, Used: used})
	}
	return p, fixed
}

// spellingIndex помнит, как каждое значение уже писали и сколько раз.
type spellingIndex map[string]map[string]int

// canonical возвращает самое частое написание значения, или само значение,
// когда такого в базе еще не было. Частота, а не первое встреченное: одна
// опечатка среди сотни записей не должна становиться нормой.
func (idx spellingIndex) canonical(field, typed string) string {
	counts := idx[field+"\x00"+NormalizeWord(typed)]
	best, bestN := typed, 0
	for spelling, n := range counts {
		// При равной частоте выигрывает написание, которое меньше по порядку:
		// иначе выбор зависел бы от порядка обхода карты и менялся от запуска
		// к запуску, а с ним менялось бы и содержимое файла.
		if n > bestN || (n == bestN && spelling < best) {
			best, bestN = spelling, n
		}
	}
	return best
}

func spellings(existing []Record) spellingIndex {
	idx := spellingIndex{}
	add := func(field, value string) {
		if value == "" {
			return
		}
		key := field + "\x00" + NormalizeWord(value)
		if idx[key] == nil {
			idx[key] = map[string]int{}
		}
		idx[key][value]++
	}
	for _, rec := range existing {
		tx := rec.Transaction()
		add("категория", tx.Category())
		add("подкатегория", tx.Subcategory())
		add("место", tx.Place())
		add("счет", tx.Account())
		add("источник", tx.Source())
	}
	return idx
}

// fromVocabulary достает из словаря те решения, что касаются написания: как
// зовется место и как зовется счет. Категорию словарь тоже знает, но подставлять
// ее значило бы менять смысл записи, а не ее написание, — а это другое решение,
// и принимать его молча нельзя.
func fromVocabulary(voc Vocabulary) map[string]string {
	out := map[string]string{}
	for word, rule := range voc.Places {
		if rule.Place != "" {
			out["место\x00"+NormalizeWord(word)] = rule.Place
			out["место\x00"+NormalizeWord(rule.Place)] = rule.Place
		}
	}
	for word, account := range voc.Accounts {
		if account != "" {
			out["счет\x00"+NormalizeWord(word)] = account
			out["счет\x00"+NormalizeWord(account)] = account
		}
	}
	return out
}
