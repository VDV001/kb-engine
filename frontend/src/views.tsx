import { useState } from 'react'
import { api } from './api'
import type { Stats } from './api'
import { useResource } from './hooks/useResource'
import { Card, Label, Stat } from './components/ui'

export function SettingsView({ stats }: { stats: Stats }) {
  // Changelog не критичен для этого вида: без него просто нет версии и трёх
  // последних релизов, поэтому падение запроса рендерится как отсутствие
  // данных, а не как ошибка страницы.
  const res = useResource(api.changelog)
  const log = res.status === 'ready' ? res.data : null

  const [fullHistory, setFullHistory] = useState(false)
  const boxes = Object.entries(stats.by_category).sort((a, b) => b[1] - a[1])
  const empty = boxes.filter(([, n]) => n === 0).length
  const all = log?.releases ?? []
  const latest = fullHistory ? all : all.slice(0, 3)

  return (
    <div className="space-y-6">
      <header>
        <Label className="text-secondary">Визуализация и кастомизация</Label>
        <h1 className="mt-1 text-4xl">Настройки базы.</h1>
        <p className="mt-2 text-sm text-on-surface-variant">
          Каталог знаний как физический артефакт. Каждый ящик — категория.
        </p>
      </header>

      <div className="flex flex-col gap-8 xl:flex-row">
        {/* Ящики: структура — прямые углы, стопка с волосяными разделителями. */}
        <div className="min-w-0 flex-1 divide-y divide-outline-variant border border-outline-variant bg-surface-low">
          {boxes.map(([cat, n]) => (
            <div key={cat} className="flex items-center justify-between gap-3 px-5 py-4">
              <span className="truncate text-sm" title={cat}>
                {cat}
              </span>
              <span className="shrink-0 rounded-full bg-secondary px-2.5 py-0.5 font-mono text-xs font-bold text-white tabular-nums">
                {n}
              </span>
            </div>
          ))}
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
                  ['Версия каталога', log?.current_version ? `v${log.current_version} · ${log.current_date ?? '—'}` : '—'],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex items-center justify-between py-2">
                  <dt className="label">{k}</dt>
                  <dd className="font-mono text-xs tabular-nums text-secondary">{v}</dd>
                </div>
              ))}
            </dl>
            {log?.current_tagline && (
              <p className="mt-2 text-xs italic text-on-surface-variant">{log.current_tagline}</p>
            )}
          </Card>

          <div className="grid grid-cols-2 gap-4">
            <Stat label="Активные ящики" value={boxes.length - empty} />
            <Stat label="Пустые ящики" value={empty} tone="muted" />
          </div>

          {latest.length > 0 && (
            <Card>
              <Label>Что нового</Label>
              <span className="ml-2 font-mono text-[10px] text-on-surface-variant">из CHANGELOG.md</span>
              <div className="mt-3 space-y-4">
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

          {/* Что впереди. Список не скопирован из исходного дашборда: там в
              «скоро» до сих пор числятся lifecycle и публичный дашборд, а
              движок делает и то, и другое — обещать сделанное значит врать
              про собственную зрелость. */}
          <Card>
            <h2 className="text-xl">Скоро</h2>
            <p className="mt-2 text-sm leading-relaxed text-on-surface-variant">
              Семантический поиск по базе, экспорт каталога, печатная версия страницы
              проектов и словарь тем, который разложит теговый хвост.
            </p>
            <div className="mt-4 flex flex-wrap gap-2">
              {['Семантический поиск', 'Экспорт', 'PDF проектов', 'Словарь тем'].map((t) => (
                <span
                  key={t}
                  className="rounded border border-outline-variant bg-surface-low px-3 py-1 font-label text-[10px] uppercase tracking-wider text-on-surface-variant"
                >
                  {t}
                </span>
              ))}
            </div>
          </Card>
        </aside>
      </div>

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
          <h3 className="font-headline text-xl font-bold">Один источник оформления</h3>
          <p className="mt-4 text-sm leading-relaxed text-on-surface-variant">
            Палитра, шрифты и размеры заданы токенами в одном месте и раздаются
            и вебу, и терминалу. Светлая и тёмная тема — две ветки одних и тех же
            значений, а не два набора.
          </p>
        </div>
      </section>
    </div>
  )
}

// --- Finances ---------------------------------------------------------------
