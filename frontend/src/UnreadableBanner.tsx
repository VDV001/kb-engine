import { useState } from 'react'
import type { UnreadableEntry } from './api'
import { plural } from './hygiene'

/**
 * Полоса о записях, которых на экране нет.
 *
 * Раньше одна негодная запись гасила витрины целиком: разбор был
 * всё-или-ничего, и полторы тысячи прочитанных записей прятались из-за одной
 * непрочитанной. Теперь каталог читается без неё — и ровно поэтому нужна эта
 * полоса: частичные данные, поданные как полные, обманывают тише, чем пустой
 * экран.
 *
 * Висит над всеми вкладками, а не на экране здоровья: считает неполную базу
 * тот, кто смотрит на числа, а не тот, кто пришёл проверять здоровье.
 */
export function UnreadableBanner({ entries, total }: { entries: UnreadableEntry[]; total: number }) {
  const [open, setOpen] = useState(false)
  if (entries.length === 0) return null

  const n = entries.length
  // Склонение берётся у общего помощника, а не пишется здесь заново: своя
  // арифметика по остаткам была бы четвёртой копией того, что уже есть, и
  // однажды разошлась бы с остальными («11 записи»).
  const word = plural(n, ['запись', 'записи', 'записей'])
  return (
    <div className="mb-6 rounded-lg bg-error-container p-4 text-on-error-container">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="text-sm">
            Показано <b className="tabular">{total}</b> из{' '}
            <b className="tabular">{total + n}</b> — {n} {word} каталога прочитать не удалось.
          </p>
          <button
            type="button"
            className="label mt-2 underline opacity-70 hover:opacity-100"
            onClick={() => setOpen((v) => !v)}
          >
            {open ? 'Свернуть' : 'Показать какие'}
          </button>
          {open && (
            <ul className="label mt-3 space-y-1 opacity-80">
              {entries.map((e) => (
                <li key={`${e.index}-${e.id}`} className="break-words">
                  {/* id=0 значит, что и номер прочитать не удалось: тогда
                      адресом остаётся только место в файле. */}
                  {e.id > 0 ? `#${e.id}` : `запись №${e.index + 1} в файле`} — {e.reason}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
