import { useEffect } from 'react'
import type { RefObject } from 'react'

/**
 * Держит CSS-переменную равной текущей высоте элемента.
 *
 * Нужна прилипающим полосам внутри страницы: они обязаны вставать ПОД шапкой,
 * а её высота не константа — на узком экране навигация переносится и шапка
 * становится выше. Захардкоженный отступ разъезжается ровно так, как описано в
 * комментарии к самой шапке: «padding, который надо держать равным высоте, а
 * измерить её заново никто не вспомнит».
 *
 * Меряем, а не угадываем. ResizeObserver, а не событие resize: шапка меняет
 * высоту и без изменения размера окна — например когда в неё приезжает счётчик
 * записей после загрузки каталога.
 */
export function useCssVarHeight(ref: RefObject<HTMLElement | null>, varName: string) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const apply = () => {
      document.documentElement.style.setProperty(varName, `${el.getBoundingClientRect().height}px`)
    }
    apply()
    const ro = new ResizeObserver(apply)
    ro.observe(el)
    return () => {
      ro.disconnect()
      // Переменную за собой убираем: оставленная, она соврёт следующему, кто
      // на неё сошлётся, а сломается это не сразу и не очевидно.
      document.documentElement.style.removeProperty(varName)
    }
  }, [ref, varName])
}
