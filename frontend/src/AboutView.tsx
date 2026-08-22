import { useState } from 'react'
import { api } from './api'
import type { Stats, Capability } from './api'
import { categoryLabel } from './catalog'
import { useResource } from './hooks/useResource'
import { Card, Label, Stat } from './components/ui'

// О базе: что это за каталог, из чего он состоит, какой он версии и что в нём
// менялось. Вкладка называлась Summary, заголовок — «Настройки базы»,
// надзаголовок — «Визуализация и кастомизация»: три обещания на одном экране,
// и ни одно не выполнялось — настроить здесь было нечего. Настройки, которые
// есть на самом деле (тема и маска сумм), живут в шапке, рядом с тем, на что
// они действуют, и отдельной страницы под них заводить не за чем.

/**
 * engineVersionLabel сокращает псевдоверсию Go до читаемой подписи.
 *
 * Бинарь, собранный не из тега, получает от Go версию вида
 * «v0.4.1-0.20260731180902-9f258b58e907+dirty». Дата и коммит в ней дублируют
 * соседние строки карточки, а сама строка ломается на две и читается как сбой.
 * Для релиза это неважно, но движок открытый: собирать его из исходников будут
 * постоянно, и именно такую строку люди увидят первой.
 */
export function engineVersionLabel(raw: string): string {
  if (raw === '') return '—'
  const dirty = raw.endsWith('+dirty')
  const base = dirty ? raw.slice(0, -'+dirty'.length) : raw
  // Псевдоверсия: базовая версия, «-0.<время сборки>-<коммит>».
  const pseudo = base.replace(/-0\.\d{14}-[0-9a-f]+$/, '-dev')
  return dirty ? `${pseudo}+правки` : pseudo
}

export function AboutView({
  stats,
  onPickCategory,
}: {
  stats: Stats
  onPickCategory: (category: string) => void
}) {
  // Ни changelog, ни версия движка не критичны для этого вида: без них просто
  // нет соответствующих строк, поэтому падение запроса рендерится как
  // отсутствие данных, а не как ошибка страницы.
  const res = useResource(api.changelog)
  const log = res.status === 'ready' ? res.data : null
  const eng = useResource(api.engine)
  const engine = eng.status === 'ready' ? eng.data : null
  const cap = useResource(api.capabilities)
  const capabilities = cap.status === 'ready' ? cap.data : null

  const [fullHistory, setFullHistory] = useState(false)
  const labels = stats.category_labels ?? {}
  const boxes = Object.entries(stats.by_category).sort((a, b) => b[1] - a[1])
  const all = log?.releases ?? []
  const latest = fullHistory ? all : all.slice(0, 3)
  // Ноль релизов — это не версия 0.0.0, а нераспознанный файл: парсер ждёт
  // CHANGELOG.md, а рядом лежит changelog.json, и подменить один другим легко.
  // Печатать при этом «v0.0.0 · —» значит выдавать сбой чтения за факт о базе.
  const parsed = all.length > 0

  return (
    <div className="space-y-6">
      <header>
        <Label className="text-secondary">Каталог знаний</Label>
        <h1 className="mt-1 text-4xl">О базе.</h1>
        <p className="mt-2 text-sm text-on-surface-variant">
          Из чего состоит база, какой она версии и что в ней менялось. Каждый ящик —
          категория; клик открывает её в архиве.
        </p>
      </header>

      <div className="flex flex-col gap-8 xl:flex-row">
        {/* Ящики: структура — прямые углы, стопка с волосяными разделителями.
            Стопка ДВОЙНАЯ, и это не украшение, а лечение пустоты: одной колонкой
            два с половиной десятка категорий вдвое выше соседних карточек, и
            рядом с нижней половиной списка не остаётся ничего. Две стопки по
            тринадцать сходятся с правым столбцом по высоте. Ниже sm — одна
            колонка: там столбец всё равно один.
            xl:self-start — только в ряду: по умолчанию flex тянет колонку до
            высоты соседней. В колоночной раскладке self-start сузил бы блок по
            содержимому, поэтому правило и стоит за брейкпоинтом. */}
        <div className="grid min-w-0 flex-1 gap-4 sm:grid-cols-2 xl:self-start">
          {[boxes.slice(0, Math.ceil(boxes.length / 2)), boxes.slice(Math.ceil(boxes.length / 2))].map(
            (column, i) => (
              <div
                key={i}
                // min-w-0 обязателен: элемент сетки по умолчанию не сжимается
                // уже содержимого, и truncate у длинных названий переставал
                // работать — на узком экране страница уезжала вбок на 1186px
                // при окне в 420. У прежней одиночной стопки этого не было:
                // она была обычным блоком, а не элементом сетки.
                className="min-w-0 divide-y divide-outline-variant border border-outline-variant bg-surface-low"
              >
                {column.map(([cat, n]) => (
                  <button
                    key={cat}
                    type="button"
                    onClick={() => onPickCategory(cat)}
                    // Ключ уходит в подсказку, наверх — тоже ключ: фильтруется архив
                    // по нему, а читает человек название. Раньше на экране стоял ключ,
                    // хотя читаемое имя каталог несёт с собой и Архив его показывает.
                    title={`${cat} — открыть в архиве`}
                    className="flex w-full items-center justify-between gap-3 px-5 py-4 text-left transition-colors hover:bg-surface-high"
                  >
                    <span className="truncate text-sm">{categoryLabel(cat, labels)}</span>
                    <span className="shrink-0 rounded-full bg-secondary px-2.5 py-0.5 font-mono text-xs font-bold text-white tabular-nums">
                      {n}
                    </span>
                  </button>
                ))}
              </div>
            ),
          )}
        </div>

        <aside className="shrink-0 space-y-4 xl:w-96">
          <Card className="border-l-2 border-l-secondary">
            <h2 className="text-xl">Информация о базе</h2>
            <dl className="mt-3 divide-y divide-outline-variant text-sm">
              {(
                [
                  ['Записей', String(stats.total)],
                  ['Категорий', String(boxes.length)],
                  ['Источник', 'Telegram Bot + Ручное'],
                  [
                    'Версия каталога',
                    parsed
                      ? `v${log?.current_version} · ${log?.current_date ?? '—'}`
                      : 'CHANGELOG.md не разобран',
                  ],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex items-center justify-between gap-3 py-2">
                  <dt className="label">{k}</dt>
                  <dd className="text-right font-mono text-xs tabular-nums text-secondary">{v}</dd>
                </div>
              ))}
            </dl>
            {parsed && log?.current_tagline && (
              <p className="mt-2 text-xs italic text-on-surface-variant">{log.current_tagline}</p>
            )}
          </Card>

          {/* Разобранность вместо пары «активные / пустые ящики»: активных было
              ровно столько же, сколько категорий строкой выше, а пустых на живой
              базе не бывает — категория заводится под запись. Два числа, одно
              из которых дубль, а второе всегда ноль, не сообщали ничего. */}
          <div className="grid grid-cols-2 gap-4">
            <Stat
              label="Разобрано"
              value={`${Math.round((stats.health.processed / Math.max(1, stats.health.total)) * 100)}%`}
              hint={`${stats.health.processed} из ${stats.health.total}`}
            />
            <Stat
              label="С конспектом"
              value={`${Math.round((stats.health.with_notes / Math.max(1, stats.health.notes_base)) * 100)}%`}
              hint={`${stats.health.with_notes} из ${stats.health.notes_base}`}
              tone="muted"
            />
          </div>

          {/* Движок — вторая версионность, и раньше её на странице не было
              вовсе: версию сборки можно было узнать только из `kbengine
              version` в терминале. Своя история релизов у движка уже есть на
              GitHub, поэтому здесь ссылка, а не её копия в бандле. */}
          <Card>
            <h2 className="text-xl">Движок</h2>
            <dl className="mt-3 divide-y divide-outline-variant text-sm">
              {(
                [
                  ['Версия', engineVersionLabel(engine?.version ?? '')],
                  ['Сборка', engine?.commit ? engine.commit.slice(0, 7) : '—'],
                  ['Собран', engine?.built ? engine.built.slice(0, 10) : '—'],
                  ['Лицензия', 'AGPL-3.0-or-later'],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex items-center justify-between gap-3 py-2">
                  <dt className="label">{k}</dt>
                  <dd className="font-mono text-xs tabular-nums text-secondary">{v}</dd>
                </div>
              ))}
            </dl>
            <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs">
              <a
                className="underline underline-offset-2 hover:text-secondary"
                href="https://github.com/VDV001/kb-engine/releases"
                target="_blank"
                rel="noreferrer"
              >
                Что нового в движке
              </a>
              <a
                className="underline underline-offset-2 hover:text-secondary"
                href="https://github.com/VDV001/kb-engine"
                target="_blank"
                rel="noreferrer"
              >
                Исходный код
              </a>
            </div>
          </Card>
        </aside>
      </div>

      {/* История версий шла третьей карточкой в правой колонке — и была вдвое
          длиннее всего остального на странице. Из-за неё левый столбец с
          категориями кончался примерно на трети, а дальше рядом с текстом
          тянулось широкое пустое поле, которое дважды подряд прочиталось как
          поломка. Длинная часть переехала ВНИЗ во всю ширину: пустоты не
          остаётся, потому что нечему обрываться. Релизы идут сеткой — во всю
          ширину строка списка иначе растянулась бы на пол-экрана. */}
      {latest.length > 0 && (
        <Card>
          <Label>Что нового в базе</Label>
          <span className="ml-2 font-mono text-[10px] text-on-surface-variant">из CHANGELOG.md</span>
          <div className="mt-3 grid gap-8 md:grid-cols-2 xl:grid-cols-3">
            {latest.map((r, i) => (
              <div key={r.version} className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <span className="font-headline text-base font-bold">v{r.version}</span>
                  {i === 0 && (
                    <span className="rounded bg-secondary px-1.5 py-0.5 font-label text-[9px] font-bold uppercase text-white">
                      latest
                    </span>
                  )}
                  <span className="ml-auto label">{r.date ?? ''}</span>
                </div>
                {r.tagline && <p className="text-xs italic text-on-surface-variant">{r.tagline}</p>}
                {Object.entries(r.sections).map(([name, items]) => (
                  <div key={name}>
                    <span className="label text-secondary">{name}</span>
                    <ul className="mt-1 list-disc space-y-1 pl-4 text-xs text-on-surface-variant">
                      {items.slice(0, 3).map((it, j) => (
                        <li key={j}>{it.length > 160 ? `${it.slice(0, 160)}…` : it}</li>
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            ))}
          </div>
          {all.length > 3 && (
            <button
              type="button"
              onClick={() => setFullHistory((v) => !v)}
              className="mt-4 w-full rounded border border-outline-variant bg-surface-high py-2 font-label text-xs uppercase tracking-wider text-on-surface-variant transition-colors hover:text-on-surface"
            >
              {fullHistory ? 'Свернуть историю' : `Показать всю историю (${all.length})`}
            </button>
          )}
        </Card>
      )}

      {/* Редакторская секция исходника. Текст переписан под движок: старая
          «Автоматизация» описывала build_dashboard.py, которого здесь нет. */}
      <section className="mt-24 grid gap-12 border-t border-outline-variant pt-12 md:grid-cols-3">
        <div>
          <h3 className="font-headline text-xl font-bold">Категории как ящики</h3>
          <p className="mt-4 text-sm leading-relaxed text-on-surface-variant">
            Каждый ящик — категория в структуре базы. Ключ хранится в записи, читаемое
            название живёт в каталоге рядом с ним, поэтому переименование категории не
            трогает ни одной записи.
          </p>
        </div>
        <div>
          <h3 className="font-headline text-xl font-bold">Откуда приходят записи</h3>
          <p className="mt-4 text-sm leading-relaxed text-on-surface-variant">
            Телеграм-бот складывает ссылки в инбокс, движок разбирает его командой
            inbox и дописывает каталог, не перекодируя то, что в нём уже лежит.
          </p>
        </div>
        <div>
          <h3 className="font-headline text-xl font-bold">Две версии, а не одна</h3>
          <p className="mt-4 text-sm leading-relaxed text-on-surface-variant">
            У базы своя версия и свой CHANGELOG.md — они про содержимое. У движка
            своя, из сборки бинаря, — она про программу. Раньше на экране жила
            только первая, и вопрос «какая версия?» отвечался не тем числом.
          </p>
        </div>
      </section>

      {/* Статусная таблица: честное деление возможностей на боевые, лес и план.
          Данные приходят из /api/capabilities — того же среза, что сверяет
          README-гейт, поэтому витрина и README не могут разойтись. */}
      {capabilities && capabilities.length > 0 && (
        <section>
          <h2 className="font-headline text-2xl font-bold">Возможности и их зрелость</h2>
          <p className="mt-2 text-sm text-on-surface-variant">
            Статус по замеру, а не по желанию: «написано» — это ещё не «стабильно».
          </p>
          <div className="mt-4 overflow-x-auto">
            <table className="w-full border-collapse text-sm">
              <thead>
                <tr className="border-b border-outline-variant text-left">
                  <th className="py-2 pr-4 font-medium">Возможность</th>
                  <th className="py-2 pr-4 font-medium">Статус</th>
                  <th className="py-2 font-medium">Замечание</th>
                </tr>
              </thead>
              <tbody>
                {capabilities.map((c: Capability) => (
                  <tr key={c.name} className="border-b border-outline-variant/60 align-top">
                    <td className="py-2 pr-4">{c.name}</td>
                    <td className="whitespace-nowrap py-2 pr-4 font-mono text-xs">
                      {c.status === 'stable' ? '✅ stable' : c.status === 'experimental' ? '⚠️ experimental' : '🚧 roadmap'}
                    </td>
                    <td className="py-2 text-on-surface-variant">{c.note}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

    </div>
  )
}
