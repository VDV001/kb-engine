// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Header } from './Header'

// Шапка решала «показывать навигацию или бургер» порогом ширины экрана (xl),
// посчитанным когда-то под десять вкладок. Вкладок стало двенадцать, на
// финансах в шапке переключатель сумм — и при 1280–1440 px (или при масштабе
// браузера на большом мониторе) пункты ложились поверх кнопок справа. Решает
// теперь замер: не помещается список в отведённое место — бургер.

const tabs = [
  { id: 'a', label: 'Dashboard' },
  { id: 'b', label: 'Finances' },
  { id: 'c', label: 'About' },
] as const

function sized(navWidth: number, listWidth: number) {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return this.tagName === 'NAV' ? navWidth : 0
  })
  vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return this.tagName === 'UL' ? listWidth : 0
  })
}

// Переключатель темы в шапке спрашивает системную тему, а в jsdom matchMedia нет.
window.matchMedia ??= ((query: string) =>
  ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {},
     addListener() {}, removeListener() {}, dispatchEvent: () => false }) as unknown as MediaQueryList)

// Высоту шапки в CSS-переменную пишет ResizeObserver, которого в jsdom тоже нет.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('Header — навигация не ложится поверх кнопок', () => {
  it('не помещается: пункты в бургере, строка навигации скрыта', () => {
    sized(500, 900)
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    expect(screen.getByRole('navigation', { hidden: true }).getAttribute('aria-hidden')).toBe('true')
    fireEvent.click(screen.getByLabelText('Открыть меню'))
    expect(screen.getAllByRole('button', { name: 'Finances' }).length).toBeGreaterThan(0)
  })

  it('помещается: строка навигации видна, бургера нет', () => {
    sized(900, 500)
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    expect(screen.getByRole('navigation').getAttribute('aria-hidden')).toBeNull()
    expect(screen.queryByLabelText('Открыть меню')).toBeNull()
  })
})
