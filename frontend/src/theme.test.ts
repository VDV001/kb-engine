// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { applyTheme, nextTheme, resolveTheme, saveTheme, storageKey, watchTheme } from './theme'

// Which theme the page opens in has three inputs and one of them is absent on a
// first visit. Getting the precedence wrong is the kind of bug that only shows
// up on someone else's machine — mine has a stored choice, so the branch that
// reads the system preference never runs here.
describe('resolveTheme', () => {
  it('honours a stored choice over the system preference', () => {
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })

  it('follows the system when nothing is stored', () => {
    expect(resolveTheme(null, true)).toBe('dark')
    expect(resolveTheme(null, false)).toBe('light')
  })

  // localStorage is shared with every other page on the origin and survives
  // upgrades. A value this code did not write is not a theme.
  it('ignores a stored value it does not recognise', () => {
    expect(resolveTheme('Dark', true)).toBe('dark')
    expect(resolveTheme('', false)).toBe('light')
    expect(resolveTheme('midnight', true)).toBe('dark')
  })
})

describe('nextTheme', () => {
  it('alternates', () => {
    expect(nextTheme('light')).toBe('dark')
    expect(nextTheme('dark')).toBe('light')
  })
})

// В окне DVK витрина живет в рамке, и тему выбирает кнопка окна: окно пишет
// выбор в общий localStorage, рамка узнает о нем событием storage. Без
// подписки «Финансы» оставались в теме, с которой открылись.
describe('watchTheme', () => {
  // jsdom не умеет matchMedia: системная тема здесь — подставная.
  function fakeSystem(dark: boolean) {
    const listeners: (() => void)[] = []
    const mq = { matches: dark, addEventListener: (_: string, f: () => void) => listeners.push(f), removeEventListener: () => {} }
    window.matchMedia = (() => mq) as unknown as typeof window.matchMedia
    return { set(d: boolean) { mq.matches = d; listeners.forEach((f) => f()) } }
  }

  it('follows a choice written by another page on the origin', () => {
    fakeSystem(false)
    const seen: string[] = []
    const stop = watchTheme((t) => seen.push(t))
    localStorage.setItem(storageKey, 'dark')
    window.dispatchEvent(new StorageEvent('storage', { key: storageKey, newValue: 'dark' }))
    localStorage.removeItem(storageKey)
    window.dispatchEvent(new StorageEvent('storage', { key: storageKey, newValue: null }))
    window.dispatchEvent(new StorageEvent('storage', { key: 'other', newValue: 'x' }))
    stop()
    window.dispatchEvent(new StorageEvent('storage', { key: storageKey, newValue: 'dark' }))
    expect(seen).toEqual(['dark', 'light'])
  })

  it('follows the system while nothing is stored', () => {
    localStorage.removeItem(storageKey)
    const sys = fakeSystem(false)
    const seen: string[] = []
    const stop = watchTheme((t) => seen.push(t))
    sys.set(true)
    stop()
    expect(seen).toEqual(['dark'])
  })
})

// Открыть страницу не значит выбрать тему: раньше applyTheme записывал
// системную тему как выбор, и витрина переставала следовать системе и окну.
describe('applyTheme', () => {
  it('does not store the theme it applies', () => {
    localStorage.removeItem(storageKey)
    applyTheme('dark')
    expect(localStorage.getItem(storageKey)).toBeNull()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    saveTheme('light')
    expect(localStorage.getItem(storageKey)).toBe('light')
  })
})
