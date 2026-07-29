// Pure functions behind the finances view. They live apart from the component
// so they can be tested without a DOM — and because every rouble the owner
// looks at passes through them.

import type { Transaction } from './api'

// What a row with no category is called on screen. Such rows are real money and
// are never dropped from a breakdown — they get a name that says what they are.
//
// The CLI prints its own label for the same bucket. Only the wording differs;
// the arithmetic is identical, and the golden case in money.test.ts is what
// keeps it that way.
export const NO_CATEGORY = '(без категории)'

// toKopecks parses the decimal string the API sends into integer kopecks.
// Amounts never become floats: the ledger stores kopecks as int64 precisely so
// that 89.99 does not turn into 89.98999999999999 on the way to the screen.
export function toKopecks(amount: string): number {
  const [rub, kop = '0'] = amount.split('.')
  const sign = rub.startsWith('-') ? -1 : 1
  return sign * (Math.abs(parseInt(rub, 10)) * 100 + parseInt(kop.padEnd(2, '0'), 10))
}

export function formatRub(kopecks: number): string {
  const sign = kopecks < 0 ? '-' : ''
  const abs = Math.abs(kopecks)
  const rub = Math.floor(abs / 100).toLocaleString('ru-RU')
  return `${sign}${rub},${String(abs % 100).padStart(2, '0')} ₽`
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

// sumBy totals amounts in kopecks under whatever key the caller picks. An empty
// key is kept, not dropped: money with no category is still money, and omitting
// it makes the bars stop adding up to the total shown above them. The key stays
// empty here so the result compares directly against the CLI's Summarize; the
// label is applied only when drawing.
export function sumBy(txs: Transaction[], key: (t: Transaction) => string): Record<string, number> {
  const out: Record<string, number> = {}
  for (const t of txs) {
    out[key(t)] = (out[key(t)] ?? 0) + toKopecks(t.amount)
  }
  return out
}

// sumByAccount is the one breakdown that does drop empty keys: a row with no
// account is not "spent from an unnamed account", it is a row whose account
// nobody recorded. The CLI leaves those out for the same reason.
export function sumByAccount(txs: Transaction[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (const t of txs) {
    const k = t.account ?? ''
    if (k === '') continue
    out[k] = (out[k] ?? 0) + toKopecks(t.amount)
  }
  return out
}

// toRoubleBars converts a kopeck breakdown to whole roubles for BarList, which
// counts in whole units. Ties keep the CLI's ordering — biggest first, name
// breaking a draw — so the same data reads the same in both places.
export function toRoubleBars(byKopecks: Record<string, number>): Record<string, number> {
  const entries = Object.entries(byKopecks).map(([k, v]) => [k || NO_CATEGORY, Math.round(v / 100)] as const)
  entries.sort((a, b) => (b[1] - a[1]) || a[0].localeCompare(b[0]))
  return Object.fromEntries(entries)
}

// daysOfMonth lists every day in "YYYY-MM", including the ones with nothing in
// them. A gap is information: dropping empty days would make three scattered
// purchases look like a steady week.
export function daysOfMonth(month: string): string[] {
  const year = Number(month.slice(0, 4))
  const m = Number(month.slice(5, 7))
  const count = new Date(year, m, 0).getDate()
  return Array.from({ length: count }, (_, i) => `${month}-${String(i + 1).padStart(2, '0')}`)
}

// monthsBetween lists every month from first to last inclusive, so a month with
// no spending shows as a gap in the trend instead of vanishing and making the
// months either side look adjacent.
export function monthsBetween(first: string, last: string): string[] {
  if (first === '' || last === '') return []
  const out: string[] = []
  let year = Number(first.slice(0, 4))
  let month = Number(first.slice(5, 7))
  const endYear = Number(last.slice(0, 4))
  const endMonth = Number(last.slice(5, 7))
  while (year < endYear || (year === endYear && month <= endMonth)) {
    out.push(`${year}-${String(month).padStart(2, '0')}`)
    month++
    if (month > 12) {
      month = 1
      year++
    }
  }
  return out
}
