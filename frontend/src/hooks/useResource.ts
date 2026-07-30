import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '../resource'
import type { Resource } from '../resource'

export interface ResourceOptions {
  /**
   * Пока false — запрос не уходит, ресурс остаётся в состоянии `loading`.
   * Это для ленивых вкладок: граф в мета-аналитике грузится, только когда на
   * него зашли. Отдельного состояния `idle` нет намеренно — единственный
   * ленивый вызов рендерит «не начинали» и «грузим» одинаково.
   */
  enabled?: boolean
  /**
   * Что именно запрашиваем. Смена ключа — это другой запрос: ресурс вернётся
   * в `loading` и загрузится заново. Один и тот же ключ не перезапрашивается
   * никогда, даже если `load` — новая стрелка на каждый рендер родителя.
   */
  key?: string
}

/**
 * Загрузить и отдать состояние. Единственное место во фронте, где живёт
 * useEffect с запросом.
 *
 * `load` вызывается один раз на КЛЮЧ, а не один раз за монтирование: период в
 * финансах меняется, и сводку под него считает сервер. При этом ленивый граф
 * по-прежнему грузится однократно — его ключ не меняется, сколько бы раз
 * вкладку ни закрывали и ни открывали.
 */
export function useResource<T>(load: () => Promise<T>, opts: ResourceOptions = {}): Resource<T> {
  const { enabled = true, key = '' } = opts
  const [resource, setResource] = useState<Resource<T>>({ status: 'loading' })

  // Ссылка на загрузчик обновляется без перезапроса: вызывающие часто передают
  // инлайновую стрелку, и реагировать на её идентичность значило бы бить по
  // сети на каждый рендер родителя.
  const latest = useRef(load)
  latest.current = load

  // Последний ключ, который уже грузили. null — не грузили ещё ничего.
  const fetchedKey = useRef<string | null>(null)
  // Номер запроса. Ответ с чужим номером отбрасывается: период мог смениться,
  // пока запрос летел, и поздний ответ показал бы цифры уже не того периода.
  const request = useRef(0)

  useEffect(() => {
    if (!enabled || fetchedKey.current === key) return
    fetchedKey.current = key
    const id = ++request.current
    setResource({ status: 'loading' })
    latest
      .current()
      .then((data) => {
        if (request.current === id) setResource({ status: 'ready', data })
      })
      .catch((e: unknown) => {
        if (request.current === id) setResource({ status: 'failed', error: errorMessage(e) })
      })
  }, [enabled, key])

  return resource
}
