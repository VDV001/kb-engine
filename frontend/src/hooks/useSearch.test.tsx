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

  it('ответ на устаревший запрос не перетирает свежий', async () => {
    const slow = Promise.resolve([{ id: 1 }])
    const fast = Promise.resolve([{ id: 2 }])
    search.mockReturnValueOnce(slow).mockReturnValueOnce(fast)

    const { result, rerender } = renderHook(({ q }) => useSearch(q), {
      initialProps: { q: 'а' },
    })
    rerender({ q: 'аб' })
    await act(async () => {
      await slow
      await fast
    })
    await waitFor(() => expect(result.current.found).toEqual(new Set([2])))
  })
})
