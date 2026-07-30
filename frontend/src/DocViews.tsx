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
}

function DocCardView({ c }: { c: DocCard }) {
  return (
    <Card className="space-y-2">
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-headline text-base font-bold">
          {c.url ? (
            <a href={c.url} target="_blank" rel="noreferrer" className="hover:underline">
              {c.title}
            </a>
          ) : (
            c.title
          )}
        </h3>
        {c.badge && (
          <span
            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
              badgeTone[c.badge] ?? badgeTone.private
            }`}
          >
            {c.badge}
          </span>
        )}
      </div>
      {c.body && <p className="text-sm leading-relaxed text-on-surface-variant">{c.body}</p>}
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

function SectionView({ s }: { s: DocSection }) {
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
            <DocCardView key={c.title} c={c} />
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

export function DocumentView({ load, name }: { load: () => Promise<Document | null>; name: string }) {
  const res = useResource(load)
  if (res.status === 'loading')
    return <p className="p-12 text-center text-on-surface-variant">Загрузка…</p>
  // failed и ready-null рендерятся одинаково — так было до хука, и менять это
  // здесь я не стал: на 500 пользователь всё ещё читает совет про флаг,
  // который у него стоит. Развод состояний в типе уже есть, дело за рендером.
  if (res.status === 'failed' || res.data === null)
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
        <SectionView key={s.title} s={s} />
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
