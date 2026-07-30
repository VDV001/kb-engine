import { ICON_VIEWBOX, ICONS } from './icons'
import type { IconName } from './icons'

/**
 * Иконка Material Symbols как inline-SVG.
 *
 * Размер наследуется от шрифта (1em), цвет — от currentColor, поэтому иконка
 * ведёт себя как буква рядом с текстом: не надо подгонять её отдельно к каждому
 * кеглю и к каждой теме.
 *
 * Имя типизировано: опечатка не доедет до экрана пустым местом, её отвергнет
 * сборка.
 */
export function Icon({
  name,
  className = '',
  title,
}: {
  name: IconName
  className?: string
  title?: string
}) {
  return (
    <svg
      viewBox={ICON_VIEWBOX}
      className={`inline-block h-[1em] w-[1em] shrink-0 fill-current ${className}`}
      // Без подписи иконка декоративна и скрыта от читалки: рядом всегда есть
      // текст, и дублировать его вслух незачем.
      role={title ? 'img' : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
    >
      {title && <title>{title}</title>}
      <path d={ICONS[name]} />
    </svg>
  )
}
