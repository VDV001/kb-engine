import { useCallback, useEffect, useState } from 'react'
import type { RefObject } from 'react'
import { wirePath } from '../architecture'
import type { Wire } from '../architecture'
import type { ArchStep } from '../api'

export interface DrawnWire extends Wire {
  step: number
  unverified: boolean
}

/**
 * Провода между карточками выбранного сценария.
 *
 * Единственное место во фронте, где размеры берутся из DOM. Иначе никак:
 * карточки раскладывает грид по содержимому, и где именно окажется «Money»
 * при этой ширине окна, знает только браузер.
 *
 * Пересчёт идёт на смену сценария, на изменение размеров поля и на его
 * прокрутку — поле шире экрана почти всегда, и стрелки, нарисованные один раз,
 * отъезжают от карточек при первом же сдвиге.
 */
// Поиск карточки перебором, а не селектором: id узлов приходят из чужого
// файла, и CSS.escape есть не везде, где этот код запускают.
function nodeEl(field: HTMLElement, id: string): Element | undefined {
  return [...field.querySelectorAll('[data-node]')].find((el) => el.getAttribute('data-node') === id)
}

export function useWires(
  fieldRef: RefObject<HTMLElement | null>,
  steps: ArchStep[] | undefined,
): DrawnWire[] {
  const [wires, setWires] = useState<DrawnWire[]>([])

  const measure = useCallback(() => {
    const field = fieldRef.current
    if (!field || !steps || steps.length === 0) {
      setWires([])
      return
    }
    const box = field.getBoundingClientRect()
    const out: DrawnWire[] = []
    for (const s of steps) {
      const a = nodeEl(field, s.from)
      const b = nodeEl(field, s.to)
      // Шаг к узлу, которого на поле нет, просто не рисуется: провод в пустоту
      // выглядел бы связью. Сам шаг при этом остаётся в списке справа.
      if (!a || !b) continue
      const ra = a.getBoundingClientRect()
      const rb = b.getBoundingClientRect()
      // Шаг из узла в тот же узел рисовать нечем — точка вместо дуги читается
      // как грязь на экране.
      if (a === b) continue
      out.push({ ...wirePath(ra, rb, box), step: s.n, unverified: s.unverified })
    }
    setWires(out)
  }, [fieldRef, steps])

  useEffect(() => {
    measure()
    const field = fieldRef.current
    if (!field) return
    // ResizeObserver, а не только onresize окна: поле меняет высоту, когда
    // карточки перестраиваются в колонках, а окно при этом не трогают.
    // Отсутствие наблюдателя не должно ронять схему — стрелки тогда просто
    // пересчитываются реже, а не пропадают вместе со всей вкладкой.
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    ro?.observe(field)
    window.addEventListener('resize', measure)
    field.addEventListener('scroll', measure, { passive: true })
    const parent = field.parentElement
    parent?.addEventListener('scroll', measure, { passive: true })
    return () => {
      ro?.disconnect()
      window.removeEventListener('resize', measure)
      field.removeEventListener('scroll', measure)
      parent?.removeEventListener('scroll', measure)
    }
  }, [fieldRef, measure])

  return wires
}
