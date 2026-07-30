// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useResource } from './useResource'

// Пробник печатает состояние ресурса текстом — так проверяется то, что увидит
// пользователь, а не внутренности хука.
function Probe({
  load,
  enabled,
  resourceKey,
}: {
  load: () => Promise<string>
  enabled?: boolean
  resourceKey?: string
}) {
  const res = useResource(load, { enabled, key: resourceKey })
  if (res.status === 'loading') return <p>загрузка</p>
  if (res.status === 'failed') return <p>ошибка: {res.error}</p>
  return <p>данные: {res.data}</p>
}

afterEach(() => {
  // Автоочистка @testing-library включается только при globals: true, а его у
  // нас нет — без этой строки разметка прошлого теста остаётся в документе и
  // getByText находит два совпадения вместо одного.
  cleanup()
  vi.restoreAllMocks()
})

describe('useResource', () => {
  it('грузит один раз при монтировании и отдаёт данные', async () => {
    const load = vi.fn().mockResolvedValue('первое')
    render(<Probe load={load} />)

    expect(screen.getByText('загрузка')).toBeTruthy()
    await waitFor(() => expect(screen.getByText('данные: первое')).toBeTruthy())
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('не перезапрашивает при повторном рендере с тем же ключом', async () => {
    const load = vi.fn().mockResolvedValue('первое')
    const { rerender } = render(<Probe load={load} resourceKey="2026-07" />)
    await waitFor(() => expect(screen.getByText('данные: первое')).toBeTruthy())

    // Другая функция-загрузчик, но ключ тот же: перезапроса быть не должно,
    // иначе каждый рендер родителя с инлайновой стрелкой бил бы по сети.
    rerender(<Probe load={vi.fn().mockResolvedValue('второе')} resourceKey="2026-07" />)
    await waitFor(() => expect(screen.getByText('данные: первое')).toBeTruthy())
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('перезапрашивает при смене ключа', async () => {
    const first = vi.fn().mockResolvedValue('июль')
    const { rerender } = render(<Probe load={first} resourceKey="2026-07" />)
    await waitFor(() => expect(screen.getByText('данные: июль')).toBeTruthy())

    const second = vi.fn().mockResolvedValue('июнь')
    rerender(<Probe load={second} resourceKey="2026-06" />)
    await waitFor(() => expect(screen.getByText('данные: июнь')).toBeTruthy())
    expect(second).toHaveBeenCalledTimes(1)
  })

  it('на время перезапроса показывает загрузку, а не старые данные', async () => {
    const { rerender } = render(<Probe load={vi.fn().mockResolvedValue('июль')} resourceKey="2026-07" />)
    await waitFor(() => expect(screen.getByText('данные: июль')).toBeTruthy())

    // Старые данные под новым ключом — это неверные данные: подпись периода
    // уже переключилась, а цифры ещё от прошлого. Лучше честная загрузка.
    let resolve: (v: string) => void = () => {}
    rerender(<Probe load={() => new Promise<string>((r) => { resolve = r })} resourceKey="2026-06" />)
    await waitFor(() => expect(screen.getByText('загрузка')).toBeTruthy())

    resolve('июнь')
    await waitFor(() => expect(screen.getByText('данные: июнь')).toBeTruthy())
  })

  it('enabled=false не запрашивает вовсе', async () => {
    const load = vi.fn().mockResolvedValue('нет')
    const { rerender } = render(<Probe load={load} enabled={false} />)

    expect(screen.getByText('загрузка')).toBeTruthy()
    expect(load).not.toHaveBeenCalled()

    // Включили — тогда и грузим. Так работает ленивая вкладка графа.
    rerender(<Probe load={load} enabled />)
    await waitFor(() => expect(screen.getByText('данные: нет')).toBeTruthy())
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('падение запроса показывает сообщение, а не пустоту', async () => {
    render(<Probe load={vi.fn().mockRejectedValue(new Error('/api/x: 500'))} />)
    await waitFor(() => expect(screen.getByText('ошибка: /api/x: 500')).toBeTruthy())
  })

  it('ответ отменённого запроса не затирает актуальный', async () => {
    // Ключ сменился, пока первый запрос ещё шёл. Его поздний ответ обязан быть
    // отброшен: иначе на экране окажутся данные периода, который уже не выбран.
    let resolveFirst: (v: string) => void = () => {}
    const { rerender } = render(
      <Probe load={() => new Promise<string>((r) => { resolveFirst = r })} resourceKey="2026-07" />,
    )
    rerender(<Probe load={vi.fn().mockResolvedValue('июнь')} resourceKey="2026-06" />)
    await waitFor(() => expect(screen.getByText('данные: июнь')).toBeTruthy())

    resolveFirst('июль')
    await new Promise((r) => setTimeout(r, 10))
    expect(screen.getByText('данные: июнь')).toBeTruthy()
  })
})
