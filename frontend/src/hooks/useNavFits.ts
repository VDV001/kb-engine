import { useLayoutEffect, useState } from 'react'
import type { RefObject } from 'react'

/**
 * Помещается ли строка вкладок в шапку.
 *
 * Отвечает замер, а не порог ширины экрана: порог считали под десять вкладок, и
 * двенадцатая вкладка вместе с переключателем на финансах положили пункты поверх
 * кнопок справа. Пересчитывается через ResizeObserver на любое изменение
 * размеров — окно, масштаб браузера, шрифт, новая вкладка, переключатель.
 *
 * Свёрнутая строка мерится против места, которое у неё будет после разворота:
 * сейчас рядом стоит бургер, и навигация уже на его ширину плюс промежуток, а
 * отступа справа у неё нет. Мерить против суженного места значило бы оставлять
 * бургер на ширине, где строка помещается, и одна ширина давала бы две разные
 * шапки в зависимости от того, как к ней пришли.
 *
 * Ширины дробные (getBoundingClientRect): при масштабе браузера целые
 * clientWidth и scrollWidth округляют до пикселя, и список шире места на долю
 * пикселя считался бы помещающимся.
 */
export function useNavFits(refs: {
  nav: RefObject<HTMLElement | null>
  list: RefObject<HTMLElement | null>
  /** Бургер — есть только в свёрнутом виде. */
  burger: RefObject<HTMLElement | null>
  /** Логотип: его правый отступ равен отступу развёрнутой навигации. */
  logo: RefObject<HTMLElement | null>
}) {
  const [fits, setFits] = useState(true)
  useLayoutEffect(() => {
    const nav = refs.nav.current
    const list = refs.list.current
    if (!nav || !list) return
    const check = () =>
      setFits((wasFitting) => {
        let room = nav.getBoundingClientRect().width
        const burger = refs.burger.current
        if (!wasFitting && burger) {
          const gap = parseFloat(getComputedStyle(burger.parentElement ?? burger).columnGap) || 0
          const margin = parseFloat(refs.logo.current ? getComputedStyle(refs.logo.current).marginRight : '') || 0
          room += burger.getBoundingClientRect().width + gap - margin
        }
        return list.getBoundingClientRect().width <= room
      })
    check()
    const ro = new ResizeObserver(check)
    ro.observe(nav)
    ro.observe(list)
    return () => ro.disconnect()
  }, [refs.nav, refs.list, refs.burger, refs.logo])
  return fits
}
