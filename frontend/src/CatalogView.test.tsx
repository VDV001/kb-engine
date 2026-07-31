// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Entry } from './api'
import { CatalogView } from './CatalogView'

afterEach(() => {
  // Автоочистка @testing-library включается только при globals: true, а его у
  // нас нет — без этой строки разметка прошлого теста остаётся в документе.
  cleanup()
})

// Сорок записей — три страницы по пятнадцать: достаточно, чтобы уйти со
// первой и проверить, что происходит с ней при новом запросе.
const many: Entry[] = Array.from({ length: 40 }, (_, i) => ({
  id: i + 1,
  title: i === 0 ? 'Единственная про Go' : `Запись ${i + 1}`,
  category: 'meta',
  kind: 'article',
  lifecycle: 'active',
  date_added: `2026-07-${String((i % 28) + 1).padStart(2, '0')}`,
}))

// Знаменатели разные: 30 разобрано из 40 записей, 4 конспекта из 30 статей.
const health = { total: 40, processed: 30, with_notes: 4, notes_base: 30 }

const view = (search: string, onSearchChange = () => {}) =>
  render(
    <CatalogView
      entries={many}
      labels={{ meta: 'Мета: про базу' }}
      tagLabels={{}}
      pickedTag=""
      onPickedTagChange={() => {}}
      pickedCategory=""
      onPickedCategoryChange={() => {}}
      health={health}
      search={search}
      onSearchChange={onSearchChange}
    />,
  )

describe('CatalogView', () => {
  it('shows the category name from the catalog, not the key', () => {
    view('')
    expect(screen.getAllByText('Мета').length).toBeGreaterThan(0)
    expect(screen.queryByText('meta')).toBeNull()
  })

  // Стоя на третьей странице и введя запрос, найдётся одна запись — то есть
  // страниц станет меньше, чем открытая. Без сброса экран покажет пустоту.
  it('returns to the first page when the query changes', () => {
    const { rerender } = view('')
    fireEvent.click(screen.getByText('3'))
    expect(screen.getByText(/Показано 31–40/).textContent).toContain('31–40')

    rerender(
      <CatalogView entries={many} labels={{}} tagLabels={{}} pickedTag="" onPickedTagChange={() => {}} pickedCategory="" onPickedCategoryChange={() => {}} health={health} search="Go" onSearchChange={() => {}} />,
    )
    expect(screen.getByText(/Показано 1–1 из 1/).textContent).toContain('1–1 из 1')
  })

  // «Сбросить» стоит рядом с фильтрами вида, но запрос живёт в шапке. Кнопка
  // обещает сбросить фильтры целиком, поэтому обязана дотянуться и туда.
  it('clears the header query on reset', () => {
    const onSearchChange = vi.fn()
    view('Go', onSearchChange)
    fireEvent.click(screen.getByText('Сбросить'))
    expect(onSearchChange).toHaveBeenCalledWith('')
  })

  // Кнопка сброса гаснет, когда сбрасывать нечего, — включая случай, когда
  // единственный действующий фильтр это запрос из шапки.
  it('enables reset for a header-only query', () => {
    view('')
    expect((screen.getByText('Сбросить') as HTMLButtonElement).disabled).toBe(true)
    cleanup()
    view('Go')
    expect((screen.getByText('Сбросить') as HTMLButtonElement).disabled).toBe(false)
  })
})

// Карточки внизу архива: обе показывают то, что посчитано на сервере, и обе
// раньше отсутствовали в движке целиком.
describe('bottom cards', () => {
  it('shows each share against its own denominator', () => {
    view('')
    // 30 из 40 записей = 75% разобрано; 4 конспекта из 30 разобранных = 13%.
    // Второе число НЕ 10%: знаменатель тут разобранные статьи, а не каталог.
    expect(screen.getByText('75%')).toBeDefined()
    expect(screen.getByText('13%')).toBeDefined()
    expect(screen.getByText('30 из 40 записей')).toBeDefined()
    expect(screen.getByText('4 из 30 разобранных статей')).toBeDefined()
  })

  // Спотлайт берёт самую свежую запись КАТАЛОГА. При запросе, сужающем выдачу
  // до другой записи, карточка обязана остаться прежней — иначе «последнее
  // добавление» означало бы «последнее среди найденного».
  it('keeps the newest entry of the whole catalog under a query', () => {
    view('')
    const newest = screen.getByText('Последнее добавление').parentElement
    const title = newest?.querySelector('h3')?.textContent
    cleanup()
    view('Go')
    expect(
      screen.getByText('Последнее добавление').parentElement?.querySelector('h3')?.textContent,
    ).toBe(title)
  })
})
