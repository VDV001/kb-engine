// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

// Переключатель темы в шапке спрашивает системную тему, а в jsdom matchMedia нет.
window.matchMedia ??= ((query: string) =>
  ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {},
     addListener() {}, removeListener() {}, dispatchEvent: () => false }) as unknown as MediaQueryList)

// ResizeObserver, которого в jsdom нет, с ручным «сработай»: замер обязан
// пересчитываться при изменении размеров, а не только при первой отрисовке.
const observers: Array<() => void> = []
beforeEach(() => {
  observers.length = 0
  globalThis.ResizeObserver = class {
    private cb: () => void
    constructor(cb: () => void) {
      this.cb = cb
      observers.push(() => this.cb())
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})
const resize = () => act(() => observers.forEach((f) => f()))

// Размеры в CSS-пикселях: место под навигацию, ширина списка, ширина бургера.
const size = { nav: 0, list: 0, burger: 48 }
function rect(width: number) {
  return { width, height: 30, top: 0, left: 0, right: width, bottom: 30, x: 0, y: 0, toJSON() {} } as DOMRect
}
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.tagName === 'NAV') return rect(size.nav)
    if (this.tagName === 'UL' && this.closest('nav')) return rect(size.list)
    if (this.getAttribute('aria-label')?.includes('меню')) return rect(size.burger)
    return rect(0)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const nav = () => document.querySelector('header nav') as HTMLElement
const collapsed = () => nav().classList.contains('invisible')

describe('Header — навигация не ложится поверх кнопок', () => {
  it('не помещается: пункты в бургере, строка навигации скрыта', () => {
    Object.assign(size, { nav: 500, list: 900 })
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    expect(collapsed()).toBe(true)
    fireEvent.click(screen.getByLabelText('Открыть меню'))
    expect(screen.getAllByRole('button', { name: 'Finances' }).length).toBeGreaterThan(0)
  })

  it('помещается: строка навигации видна, бургера нет', () => {
    Object.assign(size, { nav: 900, list: 500 })
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    expect(collapsed()).toBe(false)
    expect(screen.queryByLabelText('Открыть меню')).toBeNull()
  })

  it('пересчитывается при изменении размеров в обе стороны', () => {
    Object.assign(size, { nav: 900, list: 500 })
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    Object.assign(size, { nav: 400 })
    resize()
    expect(collapsed()).toBe(true)
    Object.assign(size, { nav: 900 })
    resize()
    expect(collapsed()).toBe(false)
  })

  // В свёрнутом виде место навигации уже на ширину бургера, которого после
  // разворота не будет. Мерить список против этого суженного места значило бы
  // оставить бургер при ширине, на которой строка помещается: одна и та же
  // ширина давала бы две разные шапки в зависимости от того, как к ней пришли.
  it('свёрнутая навигация мерится против места, которое у неё будет', () => {
    Object.assign(size, { nav: 400, list: 600 })
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    expect(collapsed()).toBe(true)
    // Расширили: свёрнутой досталось 560, без бургера будет 560 + 48 = 608 ≥ 600.
    Object.assign(size, { nav: 560 })
    resize()
    expect(collapsed()).toBe(false)
  })

  // Открытое меню не остаётся «открытым» после того, как бургер исчез: иначе,
  // сузив окно снова, человек получал панель, которую не открывал.
  it('разворот закрывает меню бургера', () => {
    Object.assign(size, { nav: 400, list: 600 })
    render(<Header tabs={[...tabs]} current="a" onSelect={() => {}} />)
    fireEvent.click(screen.getByLabelText('Открыть меню'))
    Object.assign(size, { nav: 900 })
    resize()
    Object.assign(size, { nav: 400 })
    resize()
    expect(screen.getByLabelText('Открыть меню').getAttribute('aria-expanded')).toBe('false')
  })
})
