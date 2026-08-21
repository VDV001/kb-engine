// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSearch } from './useSearch'

const search = vi.fn()
vi.mock('../api', () => ({ api: { search: (q: string) => search(q) } }))

describe('useSearch', () => {
  beforeEach(() => {
    search.mockReset()
    search.mockResolvedValue([{ id: 3 }, { id: 7 }])
  })

  it('пустой запрос сервер не спрашивает вовсе', async () => {
    const { result } = renderHook(() => useSearch(''))
    expect(result.current.found).toBeNull()
    expect(search).not.toHaveBeenCalled()
  })

  // Одинокая решётка — начало запроса про номер, а не сам запрос: пока цифру
  // не набрали, список не должен схлопываться в пустоту. Отправить её серверу
  // значило бы получить ноль записей на каждый первый символ.
  it('одинокая решётка сервер не спрашивает', async () => {
    const { result } = renderHook(() => useSearch('#'))
    expect(result.current.found).toBeNull()
    expect(search).not.toHaveBeenCalled()
  })

  it('найденное приходит множеством id', async () => {
    const { result } = renderHook(() => useSearch('kubernetes'))
    await waitFor(() => expect(result.current.found).not.toBeNull())
    expect([...(result.current.found ?? [])]).toEqual([3, 7])
    expect(search).toHaveBeenCalledWith('kubernetes')
  })

  // Отказ сервера обязан быть виден. Молча вернувшись к полному каталогу,
  // витрина показала бы 1487 записей на запрос «телепортация» — и это
  // выглядело бы как поиск, который просто плохо ищет.
  it('отказ сервера называется, а не прячется за полным списком', async () => {
    search.mockRejectedValue(new Error('сеть недоступна'))
    const { result } = renderHook(() => useSearch('kubernetes'))
    await waitFor(() => expect(result.current.error).not.toBe(''))
    expect(result.current.error).toContain('сеть недоступна')
    expect(result.current.found).toEqual(new Set())
  })

  // Пока печатают, сервер спрашивается ОДИН раз: иначе каждая буква запроса
  // из десяти символов — это десять прогонов по всему каталогу.
  it('пока печатают, сервер спрашивают один раз', async () => {
    vi.useFakeTimers()
    try {
      const { rerender } = renderHook(({ q }) => useSearch(q), { initialProps: { q: 'а' } })
      rerender({ q: 'аб' })
      rerender({ q: 'абв' })
      await act(async () => {
        vi.advanceTimersByTime(300)
      })
      expect(search).toHaveBeenCalledTimes(1)
      expect(search).toHaveBeenCalledWith('абв')
    } finally {
      vi.useRealTimers()
    }
  })

  // Гонка ответов. Оба запроса УЖЕ ушли (между ними прошёл дебаунс), а сеть
  // порядок возврата не гарантирует: ответ на «а» может прийти после ответа на
  // «аб» и перетереть его. Тогда на экране оказалась бы выдача по запросу,
  // которого в строке поиска уже нет.
  it('ответ на устаревший запрос не перетирает свежий', async () => {
    vi.useFakeTimers()
    try {
      let resolveSlow: (v: { id: number }[]) => void = () => {}
      const slow = new Promise<{ id: number }[]>((res) => {
        resolveSlow = res
      })
      search.mockReturnValueOnce(slow).mockReturnValueOnce(Promise.resolve([{ id: 2 }]))

      const { result, rerender } = renderHook(({ q }) => useSearch(q), {
        initialProps: { q: 'а' },
      })
      await act(async () => {
        vi.advanceTimersByTime(300) // ушёл запрос про «а»
      })
      rerender({ q: 'аб' })
      await act(async () => {
        vi.advanceTimersByTime(300) // ушёл запрос про «аб» и сразу ответил
      })
      expect(result.current.found).toEqual(new Set([2]))

      await act(async () => {
        resolveSlow([{ id: 1 }]) // опоздавший ответ про «а»
        await slow
      })
      expect(result.current.found).toEqual(new Set([2]))
    } finally {
      vi.useRealTimers()
    }
  })
})
