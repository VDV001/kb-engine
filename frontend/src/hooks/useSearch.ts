/** Заглушка: поведение появится следующим коммитом. */
export function useSearch(_query: string): {
  found: Set<number> | null
  loading: boolean
  error: string
} {
  return { found: null, loading: false, error: '' }
}
