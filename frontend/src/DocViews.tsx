import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from './api'
import type { DocCard, DocSection, Document } from './api'
import { useResource } from './hooks/useResource'
import { Card, Label } from './components/ui'

// The owner's personal views — Now, Team, Projects — rendered from files the
// engine is pointed at. The schema is deliberately generic (sections of
// cards): the engine renders structure, the owner's files carry the meaning,
// and the repo never embeds anyone's content. docs/examples carries an
// anonymised fixture of the shape.

const badgeTone: Record<string, string> = {
  prod: 'bg-tag-bg-2 text-tag-text-2',
  dev: 'bg-tag-bg-1 text-tag-text-1',
  published: 'bg-tag-bg-2 text-tag-text-2',
  private: 'bg-tag-bg-3 text-tag-text-3',
  paused: 'bg-tag-bg-3 text-tag-text-3',
  // Состояния людей в команде. «Уходит» и «ушёл» — разные вещи: первое ещё
  // можно застать, второе уже нет, и по цвету это должно быть видно.
  работает: 'bg-tag-bg-2 text-tag-text-2',
  уходит: 'bg-tag-bg-4 text-tag-text-4',
  ушёл: 'bg-tag-bg-3 text-tag-text-3',
  вакансия: 'bg-tag-bg-1 text-tag-text-1',
}

function DocCardView({ c, masked = false }: { c: DocCard; masked?: boolean }) {
  return (
    <Card className="space-y-2">
      {c.eyebrow && (
        // Подпись роли идёт НАД именем: страница про то, кто за что отвечает,
        // и роль здесь — то, что ищут глазами, а имя — то, чем она занята.
        <div className="flex items-center justify-between gap-2">
          <span className="label text-secondary">{c.eyebrow}</span>
          {c.badge && (
            <span className="shrink-0 rounded-full border border-outline-variant px-2 py-0.5 font-label text-[10px] uppercase tracking-wider text-on-surface-variant">
              {c.badge}
            </span>
          )}
        </div>
      )}
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-headline text-base font-bold">
          {masked ? (
            // Заголовок карточки в этом виде — имя человека. Под маской от него
            // остаётся длина: полоса на месте имени читается как «здесь имя,
            // скрытое», а пустая строка — как сломанная карточка.
            <span className="select-none text-on-surface-variant" aria-label="имя скрыто">
              {'•'.repeat(Math.min(12, Math.max(3, c.title.length)))}
            </span>
          ) : c.url ? (
            <a href={c.url} target="_blank" rel="noreferrer" className="hover:underline">
              {c.title}
            </a>
          ) : (
            c.title
          )}
        </h3>
        {/* Бейдж рисуется один раз: с подписью-ролью он ушёл наверх, к ней. */}
        {c.badge && !c.eyebrow && (
          <span
            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
              badgeTone[c.badge] ?? badgeTone.private
            }`}
          >
            {c.badge}
          </span>
        )}
      </div>
      {/* Роль и статус остаются под маской, заметка — нет: «Тим-лид, уходит»
          говорит о положении дел, а «на нём держится вот это» — о человеке. */}
      {c.body && !masked && (
        <p className="text-sm leading-relaxed text-on-surface-variant">{c.body}</p>
      )}
      {/* Зона ответственности остаётся под маской: скрыто, КТО занимает роль,
          а не за что она отвечает — иначе страница перестаёт быть моделью. */}
      {(c.points ?? []).length > 0 && (
        <ul className="space-y-1.5 pt-1">
          {c.points!.map((p) => (
            <li key={p} className="flex gap-2 text-sm leading-relaxed text-on-surface-variant">
              <span className="select-none text-secondary">·</span>
              <span>{p}</span>
            </li>
          ))}
        </ul>
      )}
      {c.meta && <p className="label">{c.meta}</p>}
      {(c.tags ?? []).length > 0 && (
        <div className="flex flex-wrap gap-1.5 pt-1">
          {c.tags!.map((t) => (
            <span key={t} className="rounded bg-surface-high px-2 py-0.5 font-label text-[10px] uppercase text-on-surface-variant">
              {t}
            </span>
          ))}
        </div>
      )}
    </Card>
  )
}

function SectionView({ s, masked = false }: { s: DocSection; masked?: boolean }) {
  const cols =
    (s.cards?.length ?? 0) >= 4
      ? 'sm:grid-cols-2 xl:grid-cols-3'
      : 'sm:grid-cols-2'
  return (
    <section className="space-y-4">
      <div className="flex items-center gap-3">
        <span className="label">{s.title}</span>
        <span className="h-px flex-1 bg-outline-variant" />
      </div>
      {s.note && <p className="max-w-2xl text-sm text-on-surface-variant">{s.note}</p>}
      {(s.cards ?? []).length > 0 && (
        <div className={`grid gap-4 ${cols}`}>
          {s.cards!.map((c) => (
            // Маска действует только там, где файл сам сказал «здесь люди».
            <DocCardView key={c.title} c={c} masked={masked && s.sensitive === true} />
          ))}
        </div>
      )}
    </section>
  )
}

/** Вид не настроен: сервер ответил null, либо запрос не дошёл. */
function NotConfigured({ name, hint }: { name: string; hint: string }) {
  return (
    <p className="p-12 text-center text-sm text-on-surface-variant">
      Вид «{name}» не настроен — {hint}
    </p>
  )
}

/** Файл указан, но прочитать его не вышло. Причина — от сервера, дословно. */
function LoadFailed({ name, error }: { name: string; error: string }) {
  // Отличить «это не JSON» от прочих сбоев можно только по тексту: сервер
  // отвечает одним и тем же кодом. Формат — единственный случай, где можно
  // сказать, ЧТО чинить, поэтому он и разбирается отдельно.
  const notJSON = error.includes('not valid JSON')
  return (
    <div className="mx-auto max-w-xl p-12 text-center text-sm text-on-surface-variant">
      {notJSON ? (
        <>
          <p>
            Файл для вида «{name}» найден, но не разобран: это не JSON. Флаг ждёт{' '}
            <code className="tabular">team.json</code> — структуру, а не markdown.
          </p>
          <p className="mt-2">
            Пример формы — <code className="tabular">docs/examples</code> в репозитории движка.
          </p>
        </>
      ) : (
        <>
          <p>Вид «{name}» не загрузился.</p>
          <p className="mt-2 font-mono text-xs text-secondary">{error}</p>
        </>
      )}
    </div>
  )
}

export function DocumentView({
  load,
  name,
  masked = false,
}: {
  load: () => Promise<Document | null>
  name: string
  masked?: boolean
}) {
  const res = useResource(load)
  if (res.status === 'loading')
    return <p className="p-12 text-center text-on-surface-variant">Загрузка…</p>
  // Три состояния, а не одно. Раньше сбой чтения и отсутствие настройки
  // рендерились одинаково — советом добавить флаг, который у человека уже
  // стоял: на живом запуске --team был указан, файл существовал, но оказался
  // markdown вместо JSON. Совет чинить сделанное хуже, чем молчание.
  if (res.status === 'failed') return <LoadFailed name={name} error={res.error} />
  if (res.data === null)
    return (
      <NotConfigured name={name} hint="запустите serve с соответствующим флагом (--team / --projects)." />
    )
  const doc = res.data
  return (
    <div className="space-y-10">
      <header>
        {doc.label && <Label className="text-secondary">{doc.label}</Label>}
        <h1 className="mt-1 text-4xl">{doc.title ?? name}</h1>
        {doc.subtitle && <p className="mt-2 max-w-2xl text-sm text-on-surface-variant">{doc.subtitle}</p>}
      </header>
      {(doc.sections ?? []).map((s) => (
        <SectionView key={s.title} s={s} masked={masked} />
      ))}
    </div>
  )
}

export function NowView() {
  const res = useResource(api.now)

  if (res.status === 'loading')
    return <p className="p-12 text-center text-on-surface-variant">Загрузка…</p>
  if (res.status === 'failed' || res.data === null)
    return <NotConfigured name="Now" hint="укажите --now путь к active-pipeline.md." />
  const now = res.data
  return (
    <div className="space-y-6">
      <header>
        <Label className="text-secondary">Active backlog</Label>
        <h1 className="mt-1 text-4xl">Now</h1>
        <p className="mt-2 text-sm text-on-surface-variant">
          Что в работе сейчас. Источник: <code className="tabular">active-pipeline.md</code>, читается при
          каждом обращении.
        </p>
      </header>
      {/* prose-стили руками: tailwind typography — ещё одна зависимость ради
          одного view, а нужных правил здесь полтора десятка. */}
      <div className="now-prose max-w-4xl">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{now.markdown}</ReactMarkdown>
      </div>
    </div>
  )
}
