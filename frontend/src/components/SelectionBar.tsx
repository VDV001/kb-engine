import type { Selection } from '../selection'
import { entriesWord } from '../catalog'

/**
 * Полоса над архивом: по какому запросу собран список и сколько за ним осталось.
 *
 * Числа два, и оба обязательны. Одно «80 записей» неотличимо от всей базы, а
 * «из 1573» без первого не говорит, что список отобран.
 *
 * Сказано «на экране», а не «нашлось по запросу»: к запросу могут быть добавлены
 * категория, тег и статус, и тогда число — их пересечение. «Нашлось по запросу
 * 80» в такой момент было бы неправдой ровно того сорта, ради которого полосу и
 * заводили.
 *
 * Отметка про агента ставится только когда ссылка объявила себя сама
 * (`src=mcp`): выводить её из наличия запроса значило бы называть ответом
 * агента любой поиск, набранный руками.
 */
export function SelectionBar({
  selection,
  onReset,
}: {
  selection: Selection | null
  onReset: () => void
}) {
  if (!selection) return null
  const { query, shown, total, fromAgent } = selection
  return (
    <div
      data-testid="selection-bar"
      className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-1 border-l-2 border-secondary bg-surface-low px-4 py-2.5 text-sm text-on-surface-variant"
    >
      <span className="min-w-0">
        {fromAgent && (
          <span className="label mr-2 rounded bg-secondary-container px-2 py-0.5 text-on-secondary-container">
            ответ агента
          </span>
        )}
        выборка по запросу <b className="text-on-surface">«{query}»</b>
        {shown === null ? (
          // Пока считаем — так и сказано. Прошлое число здесь читалось бы как
          // сегодняшнее, а число из полного списка — как найденное.
          <> — считаем…</>
        ) : (
          <>
            {' '}
            — на экране <b className="tabular text-on-surface">{shown}</b> {entriesWord(shown)} из{' '}
            <b className="tabular">{total}</b>
          </>
        )}
      </span>
      <button
        type="button"
        onClick={onReset}
        className="label ml-auto underline opacity-70 hover:opacity-100"
      >
        Показать все
      </button>
    </div>
  )
}
