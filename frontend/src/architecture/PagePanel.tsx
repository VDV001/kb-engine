import { useCallback, useState } from 'react'
import { Card } from '../components/ui'

/**
 * Собственный разбор проекта — страница, написанная человеком, прямо в карте.
 *
 * Карта умеет сказать, что с чем связано и на какой строке кода это стоит. Чего
 * она не умеет — рассказать, что делалось по неделям, зачем каждая технология и
 * где ошиблись. Это пишется прозой и рисуется руками, и такая страница уже
 * лежит в базе знаний. Раздел показывает её здесь, а не уводит по ссылке:
 * уведённый читатель обратно не возвращается.
 *
 * Страница грузится iframe'ом через маршрут /kb/, то есть открывается только
 * потому, что каталог называет её путь. Это не окно в файловую систему.
 *
 * Высота подгоняется под содержимое. Внутренняя полоса прокрутки внутри
 * страницы, у которой снаружи есть своя, читается как поломка вёрстки, а не
 * как длинный документ.
 */
export function PagePanel({ page }: { page: string }) {
  const [height, setHeight] = useState(600)
  const [failed, setFailed] = useState(false)

  const fit = useCallback((frame: HTMLIFrameElement | null) => {
    if (!frame) return
    const measure = () => {
      // Тот же origin, что и у витрины, поэтому документ читается напрямую.
      // Ошибка здесь не должна ронять вкладку: страница просто останется той
      // высоты, что была, и это лучше пустого места.
      try {
        const doc = frame.contentDocument
        if (!doc) {
          setFailed(true)
          return
        }
        const h = Math.max(
          doc.documentElement.scrollHeight,
          doc.documentElement.offsetHeight,
          doc.body?.scrollHeight ?? 0,
          doc.body?.offsetHeight ?? 0,
        )
        // Округление вверх плюс запас: дробная высота документа обрезается при
        // приведении к целым пикселям, и не хватает как раз того волоска, из-за
        // которого браузер рисует полосу прокрутки на всю высоту страницы.
        if (h > 0) setHeight(Math.ceil(h) + 2)
      } catch {
        setFailed(true)
      }
    }
    frame.addEventListener('load', () => {
      measure()
      // Шрифты и картинки доезжают после load и меняют высоту. Наблюдатель
      // вместо одного отложенного замера: у страницы с тремя диаграммами и
      // своими шрифтами высота меняется не один раз, и промахнуться на любом
      // из этих раз значит вернуть ту самую полосу.
      const doc = frame.contentDocument
      if (!doc?.documentElement) return
      const ro = new ResizeObserver(measure)
      ro.observe(doc.documentElement)
      if (doc.body) ro.observe(doc.body)
      // Наблюдатель живёт столько же, сколько документ внутри рамки: при уходе
      // с раздела React снимет саму рамку, и наблюдать станет нечего.
      frame.addEventListener('unload', () => ro.disconnect())
    })
  }, [])

  return (
    <div className="space-y-3">
      <Card>
        <p className="text-sm text-on-surface-variant">
          Разбор написан человеком и лежит в базе знаний:{' '}
          <span className="font-mono text-xs">{page}</span>. Соседние разделы говорят
          то же механически — узлами и якорями; здесь то, чего механика сказать не умеет.
        </p>
      </Card>
      {failed ? (
        <Card>
          <p className="text-sm text-on-surface-variant">
            Страницу не удалось прочитать. Проверь, что путь стоит в поле{' '}
            <span className="font-mono">file</span> какой-нибудь записи каталога —
            маршрут <span className="font-mono">/kb/</span> отдаёт только названное
            каталогом.
          </p>
        </Card>
      ) : (
        <iframe
          ref={fit}
          src={`/kb/${page}`}
          title="Разбор проекта"
          className="block w-full rounded-lg border border-outline-variant bg-surface"
          // scrolling="no" устарел по спецификации и остаётся единственным, что
          // браузеры слушают: overflow:hidden на самой рамке полосу внутри
          // документа не убирает — она принадлежит вложенному документу, а не
          // элементу. Высота при этом всё равно подгоняется: рамка без полосы и
          // без запаса по высоте просто обрезала бы хвост страницы.
          scrolling="no"
          style={{ height, overflow: 'hidden' }}
          loading="lazy"
        />
      )}
    </div>
  )
}
