package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// AccountsSource is what the finances screen needs about balances: what the
// banks say now, and the one way to change it. Declared here, in the consumer,
// like every other interface this screen uses.
//
// Reading and writing sit in one interface because they are one question — a
// balance is only worth changing on a screen that shows what it currently is.
type AccountsSource interface {
	// Balances отдаёт остатки, уже посчитанные: подтверждённое число, сколько
	// ушло после подтверждения и что осталось. Считает usecase, а не экран —
	// иначе терминал и веб посчитали бы по-разному, и разошлись бы молча.
	Balances() ([]finance.AccountBalance, error)
	SetBalance(bank string, amount domain.Money) error
	// AddAccount заводит счёт, которого на листе ещё нет. Отдельный метод, а не
	// флаг у SetBalance: экран обязан различать «поправил число» и «пополнил
	// словарь, решающий, что считается счётом во всей книге».
	AddAccount(bank string, amount domain.Money) error
}

// WithAccounts attaches the balances. Without them the key is absent and the
// block is not drawn at all, rather than showing an empty list that reads as
// "no accounts".
func (m Model) WithAccounts(a AccountsSource) Model {
	m.accounts = a
	return m
}

// balanceForm is the two-field entry: which account, and what it now holds.
type balanceForm struct {
	open   bool
	fields []field
	cursor int
	err    string
	// offerCreate — движок сказал «такого счёта на листе нет», и форма ждёт
	// ctrl+n. Заводить молча нельзя: незнакомое имя чаще опечатка, чем новый
	// счёт, и каждая промашка в раскладке пополняла бы словарь книги.
	offerCreate bool
	// chosen — имя счёта пришло из книги, а не из-под пальцев. Тогда первая
	// набранная буква заменяет его целиком: дописывать к подставленному имени
	// человек не собирался, он начал набирать другое.
	chosen bool
}

// OnBalanceForm reports whether the balance entry is open.
func (m Model) OnBalanceForm() bool { return m.balance.open }

func (m Model) openBalance() Model {
	if m.accounts == nil {
		return m
	}
	m = m.refreshAccounts()
	m.balance = balanceForm{
		open:   true,
		fields: []field{{label: fieldAccount}, {label: fieldBalance}},
	}
	// Форма открывается на первом счёте книги, а не на пустом поле. Набирать
	// имя, которое движок только что показал списком, — работа, которой не
	// должно быть, и каждая буква в ней может стать опечаткой.
	if len(m.accountSnapshot) > 0 && m.accountErr == nil {
		m.balance.fields[0].value = m.accountSnapshot[0].Bank
		m.balance.chosen = true
	}
	m.finStatus = ""
	return m
}

// walkAccounts переводит выбор на соседний счёт книги.
//
// По кругу: тупик в конце списка человек читает как поломку клавиши, а не как
// конец перечня. Счёт, набранный руками, в списке не находится — тогда шаг
// отсчитывается от начала, и это правильно: выбор из книги отменяет набранное
// именно потому, что человек его позвал.
func (m Model) walkAccounts(step int) Model {
	list := m.accountSnapshot
	if len(list) == 0 || m.accountErr != nil {
		return m
	}
	at := -1
	for i, a := range list {
		if domain.SameAccountName(a.Bank, m.balance.fields[0].value) {
			at = i
			break
		}
	}
	next := ((at+step)%len(list) + len(list)) % len(list)

	// Слайс полей копируется перед правкой. Model передаётся по значению, но
	// слайс внутри неё — общая память: без копии «прежняя» модель меняется
	// вместе с новой, и шаг, сделанный от неё, отсчитывается не оттуда.
	fields := slices.Clone(m.balance.fields)
	fields[0].value = list[next].Bank
	m.balance.fields = fields
	// Значение пришло из книги, а не из-под пальцев: первая набранная буква
	// заменит его целиком, а не допишется к нему.
	m.balance.chosen = true
	// Прежний отказ относился к прежнему счёту: оставить его на экране рядом с
	// новым именем значит сказать неправду о том, что сейчас не так.
	m.balance.err = ""
	m.balance.offerCreate = false
	return m
}

func (m Model) updateBalance(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.balance = balanceForm{}
	case tea.KeyEnter:
		return m.submitBalance(), nil
	case tea.KeyCtrlN:
		return m.createAccount(), nil
	case tea.KeyLeft, tea.KeyRight:
		// Стрелки листают счёт только когда курсор на нём: на поле суммы они
		// сменили бы счёт, в который уйдёт набранное число, и человек об этом
		// не узнал бы — он смотрит на сумму.
		if m.balance.cursor != 0 {
			return m, nil
		}
		step := 1
		if msg.Type == tea.KeyLeft {
			step = -1
		}
		return m.walkAccounts(step), nil
	default:
		// Набор поверх подставленного имени начинает новое, а не дописывает к
		// нему: «СбербанкЗайм → Коллеге» — не то, что имел в виду человек, и
		// увидел бы он это только в отказе.
		if m.balance.chosen && m.balance.cursor == 0 && msg.Type == tea.KeyRunes {
			fields := slices.Clone(m.balance.fields)
			fields[0].value = ""
			m.balance.fields = fields
			m.balance.chosen = false
		}
		m.balance.fields, m.balance.cursor = typeIntoFields(slices.Clone(m.balance.fields), m.balance.cursor, msg)
	}
	return m, nil
}

// submitBalance records the balance, or names why it did not.
//
// A refusal leaves the form as it was, the rule the entry form already follows:
// the bank the book does not know is a correctable mistake, and clearing the
// fields would take away what has to be corrected.
func (m Model) submitBalance() Model {
	bank := strings.TrimSpace(m.balance.fields[0].value)
	raw := strings.TrimSpace(m.balance.fields[1].value)

	// parseAmount, а не domain.ParseMoney: разбор один на все формы, и отказ
	// он объясняет по-человечески. Пустое поле отделено от нечитаемого — это
	// разные положения, и второе не лечится тем же движением, что первое.
	amount, err := parseAmount(raw)
	if err != nil {
		m.balance.err = fmt.Sprintf("сумма %v", err)
		return m
	}

	// Имя, которого на листе нет, дальше не идёт: движок отверг бы его и сам,
	// но экран знает счета — они прямо над формой — и может сказать это до
	// записи, вместе с выходом. Сравнение написаний делает домен: буквенное
	// поставило бы «сбербанк» рядом со «Сбербанком».
	//
	// Судить об этом экран вправе только когда список счетов у него есть.
	// Непрочитанная книга — это «не знаю», а не «счёта нет»: на пустом снимке
	// он предложил бы завести счёт, который на листе стоит, и завёл бы второй.
	if m.knowsAccountList() && !m.knowsAccount(bank) {
		m.balance.offerCreate = true
		m.balance.err = fmt.Sprintf("счёта «%s» на листе нет — ctrl+n заведёт его с этой суммой", bank)
		return m
	}

	if err := m.accounts.SetBalance(bank, amount); err != nil {
		m.balance.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	m.balance = balanceForm{}
	m.finStatus = fmt.Sprintf("баланс: %s %s", bank, amount)
	// Снимок устарел ровно сейчас: баланс, который только что записали, должен
	// быть виден на экране, а не через один вход-выход.
	return m.refreshAccounts()
}

func (m Model) renderBalanceForm() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("баланс счёта") + "\n\n")

	for i, f := range m.balance.fields {
		// Пустое поле показывает пример — то же правило, что и в остальных
		// формах. Форма баланса из него выпадала, и цена была видна: на пустой
		// сумме экран отвечал машинным текстом вместо подсказки, чего он ждёт.
		value := f.value
		if value == "" {
			value = styleDim.Render(m.hintFor(f.label))
		}
		line := fmt.Sprintf("%-14s %s", f.label, value)
		if i == m.balance.cursor {
			b.WriteString(styleSelected.Render("▸ "+line) + "\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}

	// Что у выбранного счёта записано сейчас — прямо под формой.
	//
	// Форма закрывает собой экран финансов, где это число стоит, и закрывает
	// ровно в тот момент, когда по нему принимают решение: подтверждают баланс,
	// глядя на прежний и на дату, до которой он был верен.
	b.WriteString("\n" + styleDim.Render(m.chosenAccountNote()) + "\n")

	if m.balance.err != "" {
		b.WriteString("\n" + styleTitle.Render(m.balance.err) + "\n")
	}
	return b.String() + "\n" + styleDim.Render(hintBalanceForm)
}

// chosenAccountNote описывает счёт, который сейчас в поле: что у него записано
// и когда подтверждено — или что такого счёта на листе нет.
//
// Про незнакомое имя говорится до Enter, а не после: человек уже набрал его и
// уже знает, чего хочет, а узнать, что счёта нет, только по отказу — значит
// узнать это на один шаг позже, чем можно.
func (m Model) chosenAccountNote() string {
	name := strings.TrimSpace(m.balance.fields[0].value)
	if name == "" {
		return "счёт не выбран · ←→ листают счета книги"
	}
	for _, a := range m.accountSnapshot {
		if domain.SameAccountName(a.Bank, name) {
			when := "не подтверждался"
			if a.ConfirmedOn != "" {
				when = "подтверждён " + a.ConfirmedOn[8:] + "." + a.ConfirmedOn[5:7]
			}
			return fmt.Sprintf("сейчас записано %s · %s", human(a.Confirmed), when)
		}
	}
	if !m.knowsAccountList() {
		// Книга не прочитана: сказать «счёта нет» здесь значило бы выдать
		// незнание за факт.
		return "счета книги не прочитаны"
	}
	return fmt.Sprintf("счёта «%s» на листе нет — ctrl+n заведёт его", name)
}

// createAccount заводит счёт, о котором движок только что сказал «такого нет».
//
// Работает только после этого предложения. Без него клавиша молчит: иначе она
// стала бы вторым способом записать баланс — тем, который не сверяет написание.
func (m Model) createAccount() Model {
	if !m.balance.open || !m.balance.offerCreate {
		return m
	}
	bank := strings.TrimSpace(m.balance.fields[0].value)
	amount, err := parseAmount(m.balance.fields[1].value)
	if err != nil {
		m.balance.err = fmt.Sprintf("сумма %v", err)
		return m
	}
	if err := m.accounts.AddAccount(bank, amount); err != nil {
		// Отказ обращается так же, как отказ записи: набранное остаётся,
		// причина названа. Отдельный путь не значит отдельных правил.
		m.balance.err = fmt.Sprintf("не заведён: %v", err)
		return m
	}

	m.balance = balanceForm{}
	m.finStatus = fmt.Sprintf("новый счёт: %s %s", bank, amount)
	return m.refreshAccounts()
}

// knowsAccountList reports whether the screen actually has the sheet's contents
// to judge by: a snapshot that failed to load knows nothing, and an empty one is
// not the same fact as «no such account».
func (m Model) knowsAccountList() bool {
	return m.accountErr == nil && len(m.accountSnapshot) > 0
}

// knowsAccount reports whether the sheet already holds this account, under this
// spelling or another one.
func (m Model) knowsAccount(bank string) bool {
	for _, a := range m.accountSnapshot {
		if domain.SameAccountName(a.Bank, bank) {
			return true
		}
	}
	return false
}

// writeBalances prints what each account holds, with the date the number was
// last confirmed.
//
// A balance nobody has confirmed for months is not the same fact as one
// confirmed today, so the date is shown beside it rather than left in the file.
func (m Model) writeBalances(b *strings.Builder) {
	if m.accounts == nil {
		return
	}
	if m.accountErr != nil {
		b.WriteString(styleTitle.Render("балансы не прочитаны") + "\n")
		b.WriteString(m.accountErr.Error() + "\n\n")
		return
	}
	accs := m.accountSnapshot
	if len(accs) == 0 {
		return
	}

	// Два числа рядом: сверху остаток на сейчас, под ним — подтверждённое
	// значение с датой и тем, сколько ушло после неё. Одно вместо другого
	// показывать нельзя: подтверждённое — единственный факт, сверенный с
	// банком, а расчётное отвечает на вопрос «сколько сейчас».
	balances := accs

	var total domain.Money
	for _, x := range balances {
		total = total.Add(x.Current)
	}
	fmt.Fprintf(b, "%s %s\n", styleDim.Render("на счетах"), styleAccent.Render(human(total)))

	// Итог общий, а под ним — сколько из него свободно. Деньги на карте, деньги
	// отложенные и деньги, которых сейчас нет, потому что их занял человек, —
	// это не одна сумма, и одно число о них отвечает не на тот вопрос, ради
	// которого на него смотрят. Считает то же, что и веб: один usecase.
	groups := finance.TotalsByGroup(balances)
	if len(groups) > 1 {
		var free domain.Money
		for _, g := range groups {
			if g.Group == "" {
				free = g.Total
			}
		}
		fmt.Fprintf(b, "  %s\n", styleDim.Render("свободно "+human(free)))
	}

	for _, g := range groups {
		if g.Group != "" {
			fmt.Fprintf(b, "  %s %s\n", styleDim.Render(g.Group), styleDim.Render(human(g.Total)))
		}
		for _, x := range balances {
			// Род и короткое имя уже разобраны в usecase — витрина, разбирая
			// имя сама, однажды разберёт иначе, чем веб.
			if x.Group != g.Group {
				continue
			}
			writeBalanceLine(b, x, x.NameWithinGroup)
		}
	}
	// Ограничение названо вслух: доходу домен не даёт счёта, поэтому поступления
	// в расчёт не входят и остаток может быть занижен. Умолчать об этом значило
	// бы выдать оценку за факт.
	b.WriteString(styleDim.Render("  доходы в расчёт не входят — у них нет счёта") + "\n")
	b.WriteString("\n")
}

// writeBalanceLine печатает один счёт: остаток на сейчас, а под ним —
// подтверждённое число с датой и тем, сколько ушло после неё.
//
// Имя приходит параметром: внутри рода счёт зовётся коротко, потому что слово
// рода уже стоит строкой выше, а колонка в терминале шириной в 22 знака.
func writeBalanceLine(b *strings.Builder, x finance.AccountBalance, name string) {
	fmt.Fprintf(b, "  %-22s %12s\n", trim(name, 22), human(x.Current))

	when := "—"
	if x.ConfirmedOn != "" {
		when = x.ConfirmedOn[8:] + "." + x.ConfirmedOn[5:7]
	}
	note := fmt.Sprintf("подтверждён %s · %s", human(x.Confirmed), when)
	if !x.Spent.IsZero() {
		note += fmt.Sprintf(" · после этого −%s", human(x.Spent))
	}
	if x.NeedsConfirmation {
		// Минус не значит долг: доходы счёта не имеют, поэтому на старом
		// подтверждении траты неизбежно съедают остаток. Число оставлено, но
		// названо тем, что оно есть, — просьбой сверить с банком.
		note += " · ⚠ пора подтвердить"
	}
	fmt.Fprintf(b, "  %s\n", styleDim.Render("  "+note))
}

// refreshAccounts перечитывает счета и запоминает результат.
//
// Единственное место, где книга читается ради экрана. Зовётся на входе и после
// записи — то есть тогда, когда данные могли измениться, а не на каждый кадр.
func (m Model) refreshAccounts() Model {
	if m.accounts == nil {
		m.accountSnapshot, m.accountErr = nil, nil
		return m
	}
	m.accountSnapshot, m.accountErr = m.accounts.Balances()
	return m
}
