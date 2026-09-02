// Pure functions behind the finances view. They live apart from the component
// so they can be tested without a DOM — and because every rouble the owner
// looks at passes through them.


// What a row with no category is called on screen. Such rows are real money and
// are never dropped from a breakdown — they get a name that says what they are.
//
// The CLI prints its own label for the same bucket. Only the wording differs;
// the arithmetic is identical, and the golden case in money.test.ts is what
// keeps it that way.

// toKopecks parses the decimal string the API sends into integer kopecks.
// Amounts never become floats: the ledger stores kopecks as int64 precisely so
// that 89.99 does not turn into 89.98999999999999 on the way to the screen.
export function toKopecks(amount: string): number {
  const [rub, kop = '0'] = amount.split('.')
  const sign = rub.startsWith('-') ? -1 : 1
  return sign * (Math.abs(parseInt(rub, 10)) * 100 + parseInt(kop.padEnd(2, '0'), 10))
}

export function formatRub(kopecks: number): string {
  return formatMoney(kopecks)
}

// formatMoney печатает сумму в её собственной единице.
//
// Валюта приходит кодом («USD»), а не значком: значок пришлось бы держать
// таблицей, и первая же незнакомая валюта показалась бы рублями — то есть
// молча соврала бы. Код незнакомым не бывает.
//
// Пустая валюта означает рубль: у счёта в валюте книги поля нет вовсе, и это
// решение движка, а не упущение (#332).
export function formatMoney(minor: number, currency = ''): string {
  const sign = minor < 0 ? '-' : ''
  const abs = Math.abs(minor)
  const whole = Math.floor(abs / 100).toLocaleString('ru-RU')
  const unit = currency === '' ? '₽' : currency
  return `${sign}${whole},${String(abs % 100).padStart(2, '0')} ${unit}`
}

// monthOf takes the "YYYY-MM" prefix of an ISO date without constructing a
// Date: parsing would drag in a timezone, and a purchase made on the 1st must
// not slide into the previous month because the browser sits west of UTC.
export function monthOf(date: string): string {
  return date.slice(0, 7)
}

const monthNames = [
  'январь', 'февраль', 'март', 'апрель', 'май', 'июнь',
  'июль', 'август', 'сентябрь', 'октябрь', 'ноябрь', 'декабрь',
]

export function monthLabel(month: string): string {
  const [year, m] = month.split('-')
  return `${monthNames[parseInt(m, 10) - 1]} ${year}`
}







/**
 * Сегодняшняя дата в формате YYYY-MM-DD по МЕСТНОМУ времени.
 *
 * `new Date().toISOString().slice(0, 10)` даёт дату по UTC: между полуночью и
 * пятью утра по книге (UTC+5) это ещё вчерашний день. Витрина из-за этого
 * показывала вчера — и в баре последней недели, и в возрасте подтверждения
 * баланса, то есть ровно в те часы, когда владелец чаще всего и записывает.
 */
export function todayLocal(now: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}
