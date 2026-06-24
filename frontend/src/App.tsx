import { useEffect, useState } from 'react'
import { api } from './api'
import type { Analytics, Audits, DuplicateGroup, Entry, Stats } from './api'
import { ErrorBox, Spinner } from './components/ui'
import { AnalyticsView, AuditsView, DuplicatesView, EntriesView, OverviewView } from './views'

type Tab = 'overview' | 'entries' | 'analytics' | 'audits' | 'duplicates'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Обзор' },
  { id: 'entries', label: 'Записи' },
  { id: 'analytics', label: 'Аналитика' },
  { id: 'audits', label: 'Аудиты' },
  { id: 'duplicates', label: 'Дубликаты' },
]

interface Data {
  stats: Stats
  entries: Entry[]
  audits: Audits
  duplicates: DuplicateGroup[]
  analytics: Analytics
}

export default function App() {
  const [tab, setTab] = useState<Tab>('overview')
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([api.stats(), api.entries(), api.audits(), api.duplicates(), api.analytics()])
      .then(([stats, entries, audits, duplicates, analytics]) =>
        setData({ stats, entries, audits, duplicates, analytics }),
      )
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  return (
    <div className="min-h-screen bg-slate-50 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <header className="border-b border-slate-200 bg-white px-6 py-4 dark:border-slate-700 dark:bg-slate-800">
        <h1 className="text-xl font-bold">kb-engine</h1>
        <p className="text-sm text-slate-500">Дашборд базы знаний</p>
      </header>

      <nav className="flex gap-1 border-b border-slate-200 bg-white px-6 dark:border-slate-700 dark:bg-slate-800">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium ${
              tab === t.id
                ? 'border-sky-500 text-sky-600'
                : 'border-transparent text-slate-500 hover:text-slate-700'
            }`}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className="mx-auto max-w-6xl p-6">
        {error && <ErrorBox message={error} />}
        {!error && !data && <Spinner />}
        {data && (
          <>
            {tab === 'overview' && <OverviewView stats={data.stats} />}
            {tab === 'entries' && <EntriesView entries={data.entries} />}
            {tab === 'analytics' && <AnalyticsView analytics={data.analytics} />}
            {tab === 'audits' && <AuditsView audits={data.audits} />}
            {tab === 'duplicates' && <DuplicatesView groups={data.duplicates} />}
          </>
        )}
      </main>
    </div>
  )
}
