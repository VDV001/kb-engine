import { readFileSync } from 'node:fs'
import { expect, it } from 'vitest'

it('показывает отсортированные количества в пути только для кабинета Ozon', () => {
  const app = readFileSync('../desktop/frontend/app.js', 'utf8')
  const columns = app.slice(app.indexOf('const SH_COLS ='), app.indexOf('const shHint ='))
  const list = app.slice(app.indexOf('function shList()'), app.indexOf('function shTable()'))
  const sh = {
    q: '', issue: '', only: null, sort: 'toCustomer', dir: 1,
    v: { source: 'ozon', products: [
      { toCustomer: null }, { toCustomer: 0 }, { toCustomer: 5 },
    ] },
  }
  const { shCols, shVal, shList } = new Function('sh', 'fmt', `${columns}\n${list}\nreturn { shCols, shVal, shList }`)(sh, String)

  expect(shCols().map(([key]) => key)).toContain('toCustomer')
  expect(shCols().map(([key]) => key)).toContain('toWarehouse')
  expect(shVal('toCustomer', sh.v.products[0])).toBe('—')
  expect(shVal('toCustomer', sh.v.products[1])).toBe('0')
  expect(shList()).toEqual([1, 2, 0])
  sh.dir = -1
  expect(shList()).toEqual([2, 1, 0])
  sh.v.source = ''
  expect(shCols().map(([key]) => key)).not.toContain('toCustomer')
  expect(shCols().map(([key]) => key)).not.toContain('toWarehouse')
})
