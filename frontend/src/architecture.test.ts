import { describe, expect, it } from 'vitest'
import {
  acceptanceState,
  anchorLabel,
  filterFlows,
  kindGlyph,
  kindLabel,
  isBrokenKind,
  legendKinds,
  laneColor,
  mapCounts,
  filterNodes,
  groupByLayer,
  kindsOf,
  parseAnchor,
  rootsOf,
  sortFindings,
  unverifiedCount,
  wirePath,
} from './architecture'
import type { ArchFlow, ArchNode } from './api'

const nodes: ArchNode[] = [
  { id: 'cmd-fin', title: 'fin', subtitle: 'деньги', layer: 'commands', kind: 'service', sources: ['cmd/kbengine/fin.go:123'] },
  { id: 'dom-money', title: 'Money', layer: 'domain', kind: 'data', sources: [] },
  { id: 'ghost', title: 'Без слоя', layer: 'nowhere', kind: 'data', sources: [] },
]

const flows: ArchFlow[] = [
  {
    id: 'expense',
    title: 'Трата',
    summary: 'команда → журнал',
    zone: 'Деньги',
    steps: [
      { n: 1, from: 'cmd-fin', to: 'dom-money', call: 'ParseMoney', source: 'internal/domain/money.go:125', unverified: false, branch: false },
      { n: 2, from: 'dom-money', to: 'cmd-fin', call: 'Add', unverified: true, why: 'живьём не прогонялось', branch: false },
    ],
  },
  {
    id: 'serve',
    title: 'Витрина',
    zone: 'Витрина',
    steps: [{ n: 1, from: 'cmd-fin', to: 'cmd-fin', call: 'NewServer', unverified: false, branch: false }],
  },
]

describe('parseAnchor', () => {
  const roots = { cowork: '~/claude-cowork', engine: '~/git/kb-engine' }

  it('отделяет строку от пути, а дерево от того и другого', () => {
    const a = parseAnchor('cowork:digest/run_digest.sh:42', roots)
    expect(a).toMatchObject({ root: 'cowork', path: 'digest/run_digest.sh', line: 42 })
    expect(a.absolute).toBe('~/claude-cowork/digest/run_digest.sh')
  })

  it('без имени дерева отдаёт путь как есть', () => {
    const a = parseAnchor('internal/domain/money.go:125', roots)
    expect(a).toMatchObject({ path: 'internal/domain/money.go', line: 125 })
    expect(a.root).toBeUndefined()
    expect(a.absolute).toBeUndefined()
  })

  // Имя дерева признаётся только объявленное в самой карте. Иначе «Package:
  // note» в тексте якоря превратился бы в корень, которого нет.
  it('не выдумывает дерево, которого карта не объявляла', () => {
    const a = parseAnchor('unknown:some/file.go:7', roots)
    expect(a).toMatchObject({ path: 'unknown:some/file.go', line: 7 })
    expect(a.root).toBeUndefined()
  })

  it('переживает якорь без строки', () => {
    expect(parseAnchor('README.md', roots)).toMatchObject({ path: 'README.md', line: undefined })
    expect(anchorLabel(parseAnchor('README.md', roots))).toBe('README.md')
  })

  it('rootsOf собирает словарь из карты', () => {
    expect(rootsOf({ roots: [{ name: 'engine', path: '~/git/kb-engine' }] })).toEqual({
      engine: '~/git/kb-engine',
    })
  })
})

describe('wirePath', () => {
  const field = { left: 0, top: 0, width: 1000, height: 500 }
  const a = { left: 100, top: 100, width: 200, height: 60 }
  const b = { left: 500, top: 300, width: 200, height: 60 }

  it('выходит из правого края вперёд и из левого назад', () => {
    // Вперёд: из правого края A (300) в левый край B (500).
    expect(wirePath(a, b, field).d).toMatch(/^M 300 130 C /)
    expect(wirePath(a, b, field).d).toMatch(/500 330$/)
    // Назад: из левого края B (500) в правый край A (300) — иначе две
    // встречные стрелки легли бы одна на другую, а таких шагов на карте много.
    expect(wirePath(b, a, field).d).toMatch(/^M 500 330 C /)
    expect(wirePath(b, a, field).d).toMatch(/300 130$/)
  })

  it('середина лежит между концами — туда садится номер шага', () => {
    const w = wirePath(a, b, field)
    expect(w.mid.x).toBeGreaterThan(300)
    expect(w.mid.x).toBeLessThan(500)
    expect(w.mid.y).toBe(230)
  })

  it('считает от рамки поля, а не от окна', () => {
    const shifted = wirePath(a, b, { left: 50, top: 20, width: 1000, height: 500 })
    expect(shifted.d).toMatch(/^M 250 110 /)
  })
})

describe('типы узлов', () => {
  // Пять типов — это не сорта, а состояния: скрипт, которого никто не зовёт,
  // выглядит на карте так же, как рабочий, и вся разница в этом слове.
  it('различает состояние и сорт', () => {
    expect(isBrokenKind('script-orphan')).toBe(true)
    expect(isBrokenKind('data-stale')).toBe(true)
    expect(isBrokenKind('script')).toBe(false)
    expect(isBrokenKind(undefined)).toBe(false)
  })

  it('называет тип по-русски, а незнакомый — им же самим', () => {
    expect(kindLabel('service')).toBe('код')
    expect(kindLabel('script-retired')).toBe('выведен из работы')
    expect(kindLabel('невиданный')).toBe('невиданный')
    expect(kindLabel(undefined)).toBe('без типа')
  })

  it('в легенде сломанные состояния идут последними', () => {
    const mixed: ArchNode[] = [
      { id: '1', title: 'a', kind: 'data-stale', sources: [] },
      { id: '2', title: 'b', kind: 'script', sources: [] },
      { id: '3', title: 'c', kind: 'actor', sources: [] },
    ]
    expect(legendKinds(mixed)).toEqual(['script', 'actor', 'data-stale'])
  })
})

describe('mapCounts', () => {
  // Половина счётчиков — про границу знания, а не про объём: без них карта
  // читается как полная.
  it('считает якоря, источники и сломанные узлы', () => {
    const c = mapCounts({
      nodes: [
        { id: 'a', title: 'A', kind: 'script', sources: ['x.sh:1'] },
        { id: 'b', title: 'B', kind: 'script-orphan', sources: [] },
      ],
      flows,
      findings: [{ id: 'f', title: 'находка' }],
      gaps: ['одно'],
      runtime_checks: ['прогон'],
      stats: { nodes: 2, flows: 2, steps: 3, unverified: 1, findings: 1, runtime_checks: 1 },
    })
    expect(c).toMatchObject({
      nodes: 2,
      nodesWithAnchor: 1,
      broken: 1,
      flows: 2,
      steps: 3,
      stepsWithSource: 1,
      unverified: 1,
      findings: 1,
      gaps: 1,
      runtimeChecks: 1,
    })
  })
})

describe('kindGlyph и laneColor', () => {
  // У одной карты пять типов узлов, у другой двенадцать. Незнакомый тип обязан
  // получить значок, иначе участник выпадает из легенды целиком.
  it('незнакомый тип получает точку, а не пустоту', () => {
    expect(kindGlyph('actor')).toBe('▲')
    expect(kindGlyph('невиданный')).toBe('•')
    expect(kindGlyph(undefined)).toBe('•')
  })

  // Слои у двух карт называются по-разному, поэтому цвет берётся по месту в
  // порядке: словарь по именам оставил бы чужую карту серой.
  it('цвет слоя идёт по кругу и не кончается', () => {
    expect(laneColor(0)).toBe('var(--lane-1)')
    expect(laneColor(7)).toBe('var(--lane-8)')
    expect(laneColor(8)).toBe('var(--lane-1)')
  })
})

describe('filterNodes', () => {
  it('фильтрует по слою и типу', () => {
    expect(filterNodes(nodes, { layer: 'domain' }).map((n) => n.id)).toEqual(['dom-money'])
    expect(filterNodes(nodes, { kind: 'data' }).map((n) => n.id)).toEqual(['dom-money', 'ghost'])
  })

  // Приходят сюда чаще всего с именем файла из трассы, а не с названием узла.
  it('ищет и по якорям тоже', () => {
    expect(filterNodes(nodes, { query: 'fin.go' }).map((n) => n.id)).toEqual(['cmd-fin'])
  })

  it('регистр не важен', () => {
    expect(filterNodes(nodes, { query: 'MONEY' }).map((n) => n.id)).toEqual(['dom-money'])
  })
})

describe('groupByLayer', () => {
  const layers = [
    { id: 'domain', title: 'Домен', order: 5 },
    { id: 'commands', title: 'Команды', order: 3 },
  ]

  it('идёт в объявленном порядке, а не в порядке узлов', () => {
    expect(groupByLayer(nodes, layers).map((g) => g.id)).toEqual(['commands', 'domain', ''])
  })

  // Узел со слоем, которого в карте нет, — это дыра в самой карте, и молча
  // терять его значит рисовать её полнее, чем она есть.
  it('узел неизвестного слоя виден отдельной группой', () => {
    const last = groupByLayer(nodes, layers).at(-1)
    expect(last?.title).toBe('Вне объявленных слоёв')
    expect(last?.nodes.map((n) => n.id)).toEqual(['ghost'])
  })

  it('пустых групп не показывает', () => {
    expect(groupByLayer([nodes[1]], layers).map((g) => g.id)).toEqual(['domain'])
  })
})

describe('filterFlows', () => {
  it('фильтрует по зоне', () => {
    expect(filterFlows(flows, { zone: 'Витрина' }).map((f) => f.id)).toEqual(['serve'])
  })

  it('ищет по тексту шагов, а не только по заголовку', () => {
    expect(filterFlows(flows, { query: 'parsemoney' }).map((f) => f.id)).toEqual(['expense'])
  })

  it('отбирает сценарии с непроверенными шагами', () => {
    expect(filterFlows(flows, { unverifiedOnly: true }).map((f) => f.id)).toEqual(['expense'])
    expect(unverifiedCount(flows[0])).toBe(1)
    expect(unverifiedCount(flows[1])).toBe(0)
  })
})

describe('kindsOf', () => {
  it('перечисляет типы в порядке появления, без повторов', () => {
    expect(kindsOf(nodes)).toEqual(['service', 'data'])
  })
})

describe('sortFindings', () => {
  it('тяжесть решает раньше статуса, открытое раньше починенного', () => {
    const sorted = sortFindings([
      { id: 'a', severity: 'medium', status: 'открыто' },
      { id: 'b', severity: 'high', status: 'починено' },
      { id: 'c', severity: 'high', status: 'открыто' },
      { id: 'd' },
    ])
    expect(sorted.map((f) => f.id)).toEqual(['c', 'b', 'a', 'd'])
  })
})

describe('acceptanceState', () => {
  const zones = [
    { name: 'A', accepted: true, flows: 1, steps: 1 },
    { name: 'B', accepted: false, flows: 1, steps: 1 },
  ]

  // Три ответа, а не два. Карта без раздела приёмки не «не принята» — сверять
  // с ней просто нечего, и галочка там обещала бы проверку, которой не было.
  it('без раздела приёмки — «сверять не с чем»', () => {
    expect(acceptanceState({ zones, acceptance: undefined })).toMatchObject({ state: 'unknown' })
  })

  it('часть зон — частичная приёмка', () => {
    expect(
      acceptanceState({ zones, acceptance: { classes_run: [], not_done: '', note: '' } }),
    ).toMatchObject({ state: 'partial', accepted: 1, total: 2 })
  })

  it('все зоны — принята целиком', () => {
    expect(
      acceptanceState({
        zones: zones.map((z) => ({ ...z, accepted: true })),
        acceptance: { classes_run: [], not_done: '', note: '' },
      }),
    ).toMatchObject({ state: 'accepted', accepted: 2, total: 2 })
  })
})
