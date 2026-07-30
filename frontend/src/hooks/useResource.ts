import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '../resource'
import type { Resource } from '../resource'

/**
 * Загрузить один раз и отдать состояние. Единственное место во фронте, где
 * живёт useEffect с запросом.
 *
 * `enabled: false` держит ресурс в состоянии `loading` и не запускает запрос —
 * это для ленивых вкладок (граф в мета-аналитике грузится, только когда на
 * него зашли). Отдельного состояния `idle` нет намеренно: единственный ленивый
 * вызов рендерит «не начинали» и «грузим» одинаково, и различать их пока
 * некому. Появится второй, которому важно — тогда и добавить.
 *
 * `load` вызывается ровно один раз за жизнь компонента, по первому `enabled`.
 * Смена самой функции результата не перезапрашивает: все вызывающие передают
 * стабильную ссылку из api, а перезагрузка по идентичности замыкания — это то,
 * из-за чего в useDoc стоял eslint-disable на exhaustive-deps.
 */
export function useResource<T>(load: () => Promise<T>, enabled = true): Resource<T> {
  const [resource, setResource] = useState<Resource<T>>({ status: 'loading' })
  const started = useRef(false)
  const latest = useRef(load)
  latest.current = load

  useEffect(() => {
    if (!enabled || started.current) return
    started.current = true
    latest.current()
      .then((data) => setResource({ status: 'ready', data }))
      .catch((e: unknown) => setResource({ status: 'failed', error: errorMessage(e) }))
  }, [enabled])

  return resource
}
