import type { PeriodTotal } from './api'
import { toKopecks } from './money'

// Чистая часть графиков: подготовка рядов. Отдельным файлом от компонентов по
// двум причинам сразу — иначе Fast Refresh перестаёт работать (файл экспортирует
// и компоненты, и функции), а тест на .ts, импортирующий .tsx, теряет типы.

export interface Bar {
  /** Ключ периода как он пришёл с сервера. */
  key: string
  /** Что подписать под столбцом. */
  label: string
  kopecks: number
  /** Выделить столбец — текущий месяц или сегодня. */
  current?: boolean
}

const MONTHS_SHORT = ['янв', 'фев', 'мар', 'апр', 'май', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек']

/** YYYY-MM → «июл 26». */
export function monthBarLabel(period: string): string {
  const [y, m] = period.split('-')
  return `${MONTHS_SHORT[Number(m) - 1] ?? m} ${y.slice(2)}`
}

/**
 * Ряд месяцев без дыр: между первым и последним месяцем с расходами
 * восстанавливаются пропущенные, иначе полугодовой перерыв нарисуется
 * соседними столбцами.
 */
export function monthBars(byMonth: PeriodTotal[], today: string): Bar[] {
  if (byMonth.length === 0) return []
  const sums = new Map(byMonth.map((m) => [m.period, toKopecks(m.total)]))
  const bars: Bar[] = []
  const [firstY, firstM] = byMonth[0].period.split('-').map(Number)
  const last = byMonth[byMonth.length - 1].period
  const currentMonth = today.slice(0, 7)
  for (let y = firstY, m = firstM; ; ) {
    const key = `${y}-${String(m).padStart(2, '0')}`
    bars.push({ key, label: monthBarLabel(key), kopecks: sums.get(key) ?? 0, current: key === currentMonth })
    if (key === last) break
    m += 1
    if (m > 12) {
      m = 1
      y += 1
    }
    // Страховка от бесконечного цикла, если last окажется раньше первого.
    if (bars.length > 600) break
  }
  return bars
}

/**
 * Последние `days` дней, включая сегодня, с нулями там, где трат не было.
 * Окно знает график, а не отчёт, — сервер отдаёт только дни с расходами.
 */
export function dayBars(byDay: PeriodTotal[], today: string, days = 31): Bar[] {
  const sums = new Map(byDay.map((d) => [d.period, toKopecks(d.total)]))
  const end = new Date(`${today}T00:00:00Z`)
  const bars: Bar[] = []
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(end)
    d.setUTCDate(d.getUTCDate() - i)
    const key = d.toISOString().slice(0, 10)
    bars.push({
      key,
      label: `${String(d.getUTCDate()).padStart(2, '0')}.${String(d.getUTCMonth() + 1).padStart(2, '0')}`,
      kopecks: sums.get(key) ?? 0,
      current: key === today,
    })
  }
  return bars
}
