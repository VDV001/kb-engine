import { useState } from 'react'
import type { ReactNode } from 'react'
import { Icon } from './Icon'

/**
 * Заголовок секции в стиле дашборда: подпись вразрядку и волосяная линия на
 * всю оставшуюся ширину.
 */
export function SectionHead({ title, right }: { title: string; right?: ReactNode }) {
  return (
    <div className="mb-6 flex items-center justify-between gap-8">
      <h3 className="label shrink-0 tracking-[0.3em] opacity-40">{title}</h3>
      <div className="h-px flex-1 bg-outline-variant opacity-20" />
      {right && <span className="label shrink-0 text-[10px] opacity-40">{right}</span>}
    </div>
  )
}

/**
 * То же самое, но сворачиваемое. Для блоков с длинной выборкой: пятнадцать
 * подкатегорий, дюжина мест и облако тегов подряд превращают страницу в
 * портянку, по которой приходится долго скроллить до журнала.
 *
 * По умолчанию открыто: дашборд существует, чтобы показывать данные, и
 * встречать пользователя рядом закрытых заголовков — значит заставлять его
 * кликать, чтобы увидеть то, зачем он пришёл. Свернуть — выбор того, кому
 * конкретный разрез сейчас не нужен.
 *
 * Счётчик в заголовке остаётся видимым и в свёрнутом виде: он говорит, что
 * скрыто за строкой, и избавляет от разворачивания «просто посмотреть».
 */
export function CollapsibleSection({
  title,
  count,
  right,
  defaultOpen = true,
  children,
}: {
  title: string
  count?: number
  right?: ReactNode
  defaultOpen?: boolean
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <section>
      <div className="mb-6 flex items-center justify-between gap-8">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="label flex shrink-0 items-center gap-2 tracking-[0.3em] opacity-40 transition-opacity hover:opacity-70"
        >
          <Icon name={open ? 'keyboard_arrow_up' : 'keyboard_arrow_down'} className="text-base" />
          {title}
          {count !== undefined && <span className="tabular text-[10px] opacity-60">({count})</span>}
        </button>
        <div className="h-px flex-1 bg-outline-variant opacity-20" />
        {right && <span className="label shrink-0 text-[10px] opacity-40">{right}</span>}
      </div>
      {open && children}
    </section>
  )
}
