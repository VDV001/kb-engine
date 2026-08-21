import { useEffect, useState } from 'react'
import { api } from '../api'

/** Сколько ждать после последнего нажатия, прежде чем спрашивать сервер. */
const DEBOUNCE_MS = 200

/**
 * Ответ витрине на вопрос «какие записи подходят под текст».
 *
 * Считает не витрина: текстовый поиск живёт в usecase движка, и до #252 здесь
 * лежала вторая реализация того же правила на TypeScript — «кубернетес» давал
 * 10 записей в терминале и ноль в браузере.
 *
 * found === null означает «текст не спрашивали». Пустое множество означает
 * «спрашивали, не нашлось»: это разные ответы, и на отказе сети возвращается
 * ИМЕННО пустое множество вместе с текстом ошибки — молчаливый откат к полному
 * каталогу выдал бы неработающий поиск за плохо ищущий.
 */
export function useSearch(query: string): {
  found: Set<number> | null
  loading: boolean
  error: string
} {
  const q = query.trim()
  // Одинокая решётка — начало запроса про номер, а не сам запрос: пока цифру
  // не набрали, список не должен схлопываться в пустоту.
  const asked = q !== '' && q !== '#'

  const [found, setFound] = useState<Set<number> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!asked) {
      setFound(null)
      setLoading(false)
      setError('')
      return
    }
    // Отменяется не запрос, а его ПОСЛЕДСТВИЯ: пока ответ летит, человек уже
    // набрал следующую букву, а порядок ответов сетью не гарантирован.
    let live = true
    setLoading(true)
    const timer = setTimeout(() => {
      api
        .search(q)
        .then((entries) => {
          if (!live) return
          setFound(new Set(entries.map((e) => e.id)))
          setError('')
        })
        .catch((err: unknown) => {
          if (!live) return
          setFound(new Set())
          setError(err instanceof Error ? err.message : String(err))
        })
        .finally(() => {
          if (live) setLoading(false)
        })
    }, DEBOUNCE_MS)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [q, asked])

  return { found, loading, error }
}
