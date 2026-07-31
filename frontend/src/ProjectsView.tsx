import { useState } from 'react'
import { api } from './api'
import type { Contact, Metric, ProjectCard, ProjectDoc, ProjectSection, TechGroup } from './api'
import { useResource } from './hooks/useResource'
import { badgeTone, countCards, cover, filterSections, projectFilters, tagLabel } from './projects'
import { Icon } from './components/Icon'
import type { IconName } from './components/icons'
import { ICONS } from './components/icons'

// The Projects view. Unlike the rest of the dashboard this page has an audience
// beyond the owner: he shows it to prospective clients, so every card carries
// two descriptions — `plain` says what pain the product removes, in words a
// non-developer uses, and `body` keeps the engineering detail underneath.
//
// The content lives in the owner's projects.json (--projects) and the
// screenshots in his media directory (--media). This file is the renderer and
// knows nothing about either.

/** Ширина карточки. Классы перечислены целиком, а не собираются из span:
 *  Tailwind ищет их в исходниках текстом и собранной строки не увидит. */
const SPAN_CLASS: Record<string, string> = {
  full: 'md:col-span-6',
  // На планшете треть слишком узка для описания, поэтому третей там нет.
  third: 'md:col-span-3 xl:col-span-2',
  half: 'md:col-span-3',
}

function isIcon(name: string | undefined): name is IconName {
  return name !== undefined && name in ICONS
}

/** Цифра с подписью. Одна форма и в шапке страницы, и в полосе метрик.
 *
 * Подпись переносится по словам и не обрезается: «AI-провайдера» в ячейке
 * шириной в четверть карточки иначе теряет хвост, а метрика без подписи —
 * просто число неизвестно чего. */
function Figure({ m, className = '' }: { m: Metric; className?: string }) {
  return (
    <div className={`min-w-0 ${className}`}>
      <div className="font-display text-2xl leading-none">{m.value}</div>
      <div className="label mt-1 leading-tight tracking-normal break-words text-on-surface-variant">
        {m.label}
      </div>
    </div>
  )
}

/** Колонок ровно столько, сколько метрик. Классы перечислены целиком: Tailwind
 *  ищет их в исходниках текстом и собранной строки не увидит. */
const METRIC_COLS: Record<number, string> = {
  1: 'sm:grid-cols-1',
  2: 'sm:grid-cols-2',
  3: 'sm:grid-cols-3',
  4: 'sm:grid-cols-4',
}

/**
 * Обложка карточки: скриншот продукта или градиент.
 *
 * Тёмная в обеих темах намеренно — это «обложка» продукта, а не поверхность
 * интерфейса, и белый текст поверх неё читается одинаково при любой теме.
 */
function Cover({ card, tall }: { card: ProjectCard; tall: boolean }) {
  const { className, style } = cover(card)
  const link = (card.links ?? [])[0]
  return (
    <div
      className={`relative flex min-w-0 flex-col justify-end overflow-hidden rounded-lg ${
        tall ? 'aspect-[16/10] lg:aspect-[3/2]' : 'aspect-[16/10]'
      } ${className}`}
      style={style}
    >
      {/* Сетка — отдельный слой, а не класс на самой обложке: она задаёт
          background-image, и на общем элементе затёрла бы градиент акцента.
          Ровно так обложки и оказались пустыми при первом прогоне. */}
      <div className="proj-grid absolute inset-0" />

      {/* Со снимком плашка не несёт ничего своего: ни подписи, ни затемнения.
          Название и теги стоят сразу под ней в описании, и продублированные
          поверх картинки они читались как сбой, а не как подпись. Обрезается
          только низ — пропорция плашки близка к пропорции снимка, поэтому по
          бокам ничего не режется. */}
      {card.image && (
        <img
          src={card.image}
          alt=""
          className="absolute inset-0 h-full w-full object-cover object-top"
        />
      )}

      {/* Без снимка плашка сама себе обложка: марка проекта на градиенте. */}
      {!card.image && (
        <>
          {(card.code ?? []).length > 0 && (
            <div className="absolute inset-x-6 top-6 space-y-0.5 font-mono text-[11px] leading-relaxed text-white/40">
              {card.code!.map((line) => (
                <div key={line} className="truncate">
                  {line}
                </div>
              ))}
            </div>
          )}
          <div className="relative z-10 min-w-0 p-6 sm:p-8">
            {card.kicker && <div className="label mb-2 text-white/45">{card.kicker}</div>}
            <div className="font-display text-3xl font-light break-words text-white sm:text-4xl">
              {card.short ?? card.title}
            </div>
            {(card.tags ?? []).length > 0 && (
              <div className="mt-3 flex flex-wrap gap-2">
                {card.tags!.map((t) => (
                  <span key={t} className="proj-pill-dark">
                    {t}
                  </span>
                ))}
              </div>
            )}
          </div>
        </>
      )}

      {/* Ссылка в углу — только на пустой обложке. Та же ссылка стоит под
          описанием, и две одинаковые кнопки на одной карточке — не навигация,
          а шум. */}
      {link && !card.image && (
        <a
          href={link.url}
          target="_blank"
          rel="noreferrer"
          className="proj-chip absolute top-5 right-5 z-10"
        >
          {link.label} →
        </a>
      )}
    </div>
  )
}

function CardBody({ card, compact }: { card: ProjectCard; compact: boolean }) {
  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        {card.badge && (
          <span
            className={`rounded-full px-3 py-1 font-mono text-[10px] tracking-wider uppercase ${badgeTone(card.badge)}`}
          >
            {card.badge}
          </span>
        )}
        {card.note && <span className="font-mono text-xs text-on-surface-variant">{card.note}</span>}
      </div>

      <h3 className={`leading-tight ${compact ? 'text-xl' : 'text-2xl sm:text-3xl'}`}>{card.title}</h3>

      {/* Видимое описание — одно, то, что написано словами заказчика.
          Техническое лежит под раскрытием: рядом, друг за другом, два текста
          про одно и то же читались как мусор, и владелец сказал об этом
          прямо. details — нативный, без библиотеки и без своего состояния. */}
      {card.plain && <p className="text-base leading-relaxed text-on-surface">{card.plain}</p>}
      {card.body && (
        <details className="group">
          <summary className="label cursor-pointer list-none text-secondary hover:underline">
            Технические подробности
          </summary>
          <p className="mt-2 text-sm leading-relaxed text-on-surface-variant">{card.body}</p>
        </details>
      )}

      {(card.tags ?? []).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {card.tags!.map((t) => (
            <span key={t} className="proj-pill">
              {tagLabel(t)}
            </span>
          ))}
        </div>
      )}

      {(card.metrics ?? []).length > 0 && (
        // Колонок ровно столько, сколько метрик: фиксированные четыре
        // оставляли пустую ячейку у карточек с тремя цифрами, и полоса
        // переставала читаться как одно целое.
        <div
          className={`grid grid-cols-2 overflow-hidden rounded-lg bg-surface-low ${
            METRIC_COLS[card.metrics!.length] ?? 'sm:grid-cols-4'
          }`}
        >
          {card.metrics!.map((m) => (
            <Figure key={m.label} m={m} className="proj-metric min-w-0 p-4 text-center" />
          ))}
        </div>
      )}

      {(card.links ?? []).length > 0 && (
        <div className="flex flex-wrap gap-4 pt-1">
          {card.links!.map((l) => (
            <a
              key={l.url}
              href={l.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-2 border-b-2 border-secondary pb-1 text-sm font-medium hover:pr-2"
            >
              {l.label}
              <Icon name="arrow_outward" />
            </a>
          ))}
        </div>
      )}
      {(card.links ?? []).length === 0 && card.badge === 'Private repo' && (
        <span className="inline-flex items-center gap-2 border-b border-outline-variant pb-1 text-sm text-on-surface-variant opacity-70">
          <Icon name="lock" />
          Приватный репозиторий
        </span>
      )}
    </div>
  )
}

/**
 * Карточка: снимок во всю ширину, под ним описание.
 *
 * Раньше широкая карточка ставила снимок и текст рядом колонками, и снимок в
 * трёх пятых ширины выходил мелким — на нём ничего не разобрать, а именно он
 * и объясняет продукт быстрее любого абзаца. Владелец попросил ровно это:
 * «весь блок на картинку, а ниже уже описание».
 */
function ProjectCardView({ card }: { card: ProjectCard }) {
  const span = card.span ?? 'half'
  return (
    <article className={`proj-card col-span-6 min-w-0 ${SPAN_CLASS[span] ?? SPAN_CLASS.half}`}>
      <div className="flex min-w-0 flex-col gap-6">
        <Cover card={card} tall={span === 'full'} />
        <CardBody card={card} compact={span === 'third'} />
      </div>
    </article>
  )
}

function SectionView({ s }: { s: ProjectSection }) {
  return (
    <section className="space-y-8">
      <div className="flex items-center gap-3">
        <span className="h-px w-6 bg-secondary" />
        <h2 className="text-2xl">{s.title}</h2>
      </div>
      {s.note && <p className="max-w-2xl text-sm text-on-surface-variant">{s.note}</p>}
      {/* Шесть колонок, потому что делятся и надвое, и натрое: полная карточка
          занимает все шесть, половинная три, третная две. */}
      <div className="grid grid-cols-6 gap-10">
        {(s.cards ?? []).map((c) => (
          <ProjectCardView key={c.title} card={c} />
        ))}
      </div>
    </section>
  )
}

function TechView({ groups }: { groups: TechGroup[] }) {
  return (
    <div className="grid grid-cols-2 gap-6 sm:grid-cols-3">
      {groups.map((g) => (
        <div key={g.title} className="min-w-0">
          <div className="label mb-3 text-secondary">{g.title}</div>
          <dl className="divide-y divide-outline-variant text-sm">
            {g.items.map((i) => (
              <div key={i.name} className="flex items-center justify-between gap-2 py-1.5">
                <dt className="truncate font-medium">{i.name}</dt>
                {i.value && <dd className="shrink-0 font-mono text-[10px] text-on-surface-variant">{i.value}</dd>}
              </div>
            ))}
          </dl>
        </div>
      ))}
    </div>
  )
}

function ContactsView({ contacts }: { contacts: Contact[] }) {
  return (
    <div className="space-y-3">
      {contacts.map((c) => (
        <a
          key={c.url}
          href={c.url}
          target="_blank"
          rel="noreferrer"
          className="flex items-center gap-3 rounded-lg border border-outline-variant bg-surface-low p-4 transition-colors hover:border-secondary"
        >
          {isIcon(c.icon) && <Icon name={c.icon} className="text-lg text-secondary" />}
          <div className="min-w-0">
            <div className="text-sm font-medium">{c.label}</div>
            {c.value && <div className="truncate font-mono text-[10px] text-on-surface-variant">{c.value}</div>}
          </div>
        </a>
      ))}
    </div>
  )
}

export function ProjectsView() {
  const res = useResource(api.projects)
  const [tag, setTag] = useState('')
  const [query, setQuery] = useState('')

  if (res.status === 'loading')
    return <p className="p-12 text-center text-on-surface-variant">Загрузка…</p>
  if (res.status === 'failed' || res.data === null)
    return (
      <p className="p-12 text-center text-sm text-on-surface-variant">
        Вид «Projects» не настроен — запустите serve с флагом --projects.
      </p>
    )

  const doc: ProjectDoc = res.data
  const all = doc.sections ?? []
  const shown = filterSections(all, { tag, query })
  const filters = projectFilters(all)
  const visible = countCards(shown)

  return (
    <div className="space-y-16">
      <header className="flex flex-col justify-between gap-10 lg:flex-row lg:items-end">
        <div className="min-w-0 max-w-3xl">
          <div className="mb-4 flex items-center gap-3">
            <span className="h-px w-8 bg-secondary" />
            <span className="label text-secondary">{doc.label ?? 'Projects'}</span>
          </div>
          <h1 className="text-4xl leading-[0.95] sm:text-5xl">{doc.title ?? 'Проекты'}</h1>
          {doc.subtitle && (
            <p className="mt-5 max-w-xl leading-relaxed text-on-surface-variant">{doc.subtitle}</p>
          )}
        </div>
        {(doc.stats ?? []).length > 0 && (
          <div className="grid shrink-0 grid-cols-2 gap-4 sm:grid-cols-4 lg:w-80 lg:grid-cols-2">
            {doc.stats!.map((s) => (
              <Figure
                key={s.label}
                m={s}
                className="min-w-0 rounded-lg border border-outline-variant bg-surface-low p-5"
              />
            ))}
          </div>
        )}
      </header>

      <section className="space-y-6">
        <div className="flex flex-wrap items-center gap-3 border-b border-outline-variant pb-6">
          <label className="relative">
            <Icon
              name="search"
              className="absolute top-1/2 left-3 -translate-y-1/2 text-on-surface-variant"
            />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Поиск по проектам…"
              aria-label="Поиск по проектам"
              className="w-56 rounded-md bg-surface-high py-2 pr-4 pl-9 text-sm focus:ring-1 focus:ring-secondary focus:outline-none sm:w-64"
            />
          </label>
          {/* «Все» — не тег, а его отсутствие, поэтому кнопка стоит отдельно от
              собранных из данных. */}
          <button
            type="button"
            onClick={() => setTag('')}
            aria-pressed={tag === ''}
            className={`rounded-full px-4 py-2 text-sm font-medium transition-colors ${
              tag === '' ? 'bg-primary text-on-primary' : 'bg-surface-high text-on-surface-variant hover:bg-surface-highest'
            }`}
          >
            Все
          </button>
          {filters.map((f) => (
            <button
              key={f.tag}
              type="button"
              onClick={() => setTag(f.tag)}
              aria-pressed={tag === f.tag}
              className={`rounded-full px-4 py-2 text-sm font-medium transition-colors ${
                tag === f.tag ? 'bg-primary text-on-primary' : 'bg-surface-high text-on-surface-variant hover:bg-surface-highest'
              }`}
            >
              {f.label}
            </button>
          ))}
          <span
            className="ml-auto rounded bg-secondary px-2 py-1 font-mono text-xs font-bold text-white tabular-nums"
            title="Видимых проектов"
          >
            {visible}
          </span>
        </div>

        {shown.length === 0 ? (
          <div className="py-12 text-center">
            <Icon name="search_off" className="text-4xl text-on-surface-variant" />
            <p className="mt-3 font-mono text-sm text-on-surface-variant">Проекты не найдены</p>
          </div>
        ) : (
          <div className="space-y-16">
            {shown.map((s) => (
              <SectionView key={s.title} s={s} />
            ))}
          </div>
        )}
      </section>

      {((doc.tech ?? []).length > 0 || (doc.contacts ?? []).length > 0) && (
        <div className="grid grid-cols-1 gap-10 border-t border-outline-variant pt-14 lg:grid-cols-3">
          {(doc.tech ?? []).length > 0 && (
            <div className="min-w-0 space-y-8 lg:col-span-2">
              <div className="flex items-center gap-3">
                <span className="h-px w-6 bg-secondary" />
                <h2 className="text-2xl">Технический стек</h2>
              </div>
              <TechView groups={doc.tech!} />
            </div>
          )}
          {(doc.contacts ?? []).length > 0 && (
            <div className="min-w-0 space-y-6">
              <div className="flex items-center gap-3">
                <span className="h-px w-6 bg-secondary" />
                <h2 className="text-2xl">Контакты</h2>
              </div>
              <ContactsView contacts={doc.contacts!} />
            </div>
          )}
        </div>
      )}

      {doc.footer && (
        <footer className="flex flex-col items-center justify-between gap-4 border-t border-outline-variant py-10 sm:flex-row">
          {doc.footer.name && <div className="font-display text-lg">{doc.footer.name}</div>}
          {doc.footer.tagline && <div className="label text-on-surface-variant">{doc.footer.tagline}</div>}
          {doc.footer.place && <div className="font-mono text-[10px] text-on-surface-variant">{doc.footer.place}</div>}
        </footer>
      )}
    </div>
  )
}
