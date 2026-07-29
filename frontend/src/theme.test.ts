import { describe, expect, it } from 'vitest'
import { nextTheme, resolveTheme } from './theme'

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
