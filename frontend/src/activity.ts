import type { Entry } from './api'

/** Подписи строк. Пустые — чтобы читались через одну, как в исходном дашборде. */
export const DAY_LABELS = ['Пн', '', 'Ср', '', 'Пт', '', 'Вс']

export interface ActivityDay {
  date: string
  count: number
  /** 0 — пусто, 1..4 — четверти от максимума. */
  level: number
  isFuture: boolean
}

export interface ActivityWeek {
  days: ActivityDay[]
}

export interface Activity {
  columns: ActivityWeek[]
  maxCount: number
}

/** Дата в UTC как YYYY-MM-DD. Часовой пояс здесь только помешал бы: в каталоге
 * даты записаны без времени, и локальный сдвиг двигал бы записи на сутки. */
function isoDay(d: Date): string {
  return d.toISOString().slice(0, 10)
}

/** Уровень закраски: четверти от максимума, как в исходном дашборде. */
function levelOf(count: number, max: number): number {
  if (count <= 0) return 0
  const ratio = count / max
  if (ratio <= 0.25) return 1
  if (ratio <= 0.5) return 2
  if (ratio <= 0.75) return 3
  return 4
}

/**
 * buildActivity раскладывает записи в сетку «недели × дни недели».
 *
 * Считает по дате ДОБАВЛЕНИЯ, а date_created берёт лишь как запасную. Это
 * намеренное расхождение с исходным дашбордом: тот считал по date_created, то
 * есть по дате публикации статьи, хотя подпись обещала добавленные записи —
 * разные факты, и на этой базе они расходятся (у 862 записей есть только
 * date_added, у 461 — только date_created).
 */
export function buildActivity(
  entries: Entry[],
  { weeks, today }: { weeks: number; today: Date },
): Activity {
  const counts = new Map<string, number>()
  for (const e of entries) {
    const raw = e.date_added || e.date_created
    if (!raw) continue
    const day = raw.slice(0, 10)
    counts.set(day, (counts.get(day) ?? 0) + 1)
  }

  const maxCount = Math.max(0, ...counts.values())
  const todayISO = isoDay(today)

  // Сетка начинается с понедельника, иначе первый столбец окажется рваным.
  const start = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate()))
  const mondayOffset = (start.getUTCDay() + 6) % 7
  start.setUTCDate(start.getUTCDate() - weeks * 7 - mondayOffset)

  const cursor = new Date(start)
  const columns: ActivityWeek[] = []
  for (let w = 0; w <= weeks; w++) {
    const days: ActivityDay[] = []
    for (let d = 0; d < 7; d++) {
      const date = isoDay(cursor)
      const count = counts.get(date) ?? 0
      days.push({ date, count, level: levelOf(count, maxCount), isFuture: date > todayISO })
      cursor.setUTCDate(cursor.getUTCDate() + 1)
    }
    columns.push({ days })
  }

  return { columns, maxCount }
}
