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
          doc.body?.scrollHeight ?? 0,
        )
        if (h > 0) setHeight(h)
      } catch {
        setFailed(true)
      }
    }
    frame.addEventListener('load', measure)
    // Шрифты и картинки доезжают после load и меняют высоту. Один повторный
    // замер дешевле наблюдателя и закрывает ровно этот случай.
    frame.addEventListener('load', () => window.setTimeout(measure, 400))
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
          className="w-full rounded-lg border border-outline-variant bg-surface"
          style={{ height }}
          loading="lazy"
        />
      )}
    </div>
  )
}
