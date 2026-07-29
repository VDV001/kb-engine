import { useEffect, useState } from 'react'
import { api } from './api'
import type { Analytics, AnalyticsConfig, Audits, DuplicateGroup, Entry, Finances, Stats } from './api'
import { ErrorBox, Spinner } from './components/ui'
import {
  AnalyticsView,
  ArchivesView,
  AuditsView,
  DuplicatesView,
  EntriesView,
  FinancesView,
  OverviewView,
  SettingsView,
} from './views'

type Tab = 'overview' | 'entries' | 'analytics' | 'audits' | 'duplicates' | 'archives' | 'finances' | 'settings'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Обзор' },
  { id: 'entries', label: 'Записи' },
  { id: 'analytics', label: 'Аналитика' },
  { id: 'audits', label: 'Аудиты' },
  { id: 'duplicates', label: 'Дубликаты' },
  { id: 'archives', label: 'Архив' },
  { id: 'finances', label: 'Финансы' },
  { id: 'settings', label: 'Сводка' },
]

interface Data {
  stats: Stats
  entries: Entry[]
  audits: Audits
  duplicates: DuplicateGroup[]
  analytics: Analytics
  analyticsConfig: AnalyticsConfig
  finances: Finances
}

export default function App() {
  const [tab, setTab] = useState<Tab>('overview')
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([
      api.stats(),
      api.entries(),
      api.audits(),
      api.duplicates(),
      api.analytics(),
      api.analyticsConfig(),
      api.finances(),
    ])
      .then(([stats, entries, audits, duplicates, analytics, analyticsConfig, finances]) =>
        setData({ stats, entries, audits, duplicates, analytics, analyticsConfig, finances }),
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
            {tab === 'analytics' && (
              <AnalyticsView analytics={data.analytics} config={data.analyticsConfig} />
            )}
            {tab === 'audits' && <AuditsView audits={data.audits} />}
            {tab === 'duplicates' && <DuplicatesView groups={data.duplicates} />}
            {tab === 'archives' && <ArchivesView entries={data.entries} />}
            {tab === 'finances' && <FinancesView finances={data.finances} />}
            {tab === 'settings' && <SettingsView stats={data.stats} />}
          </>
        )}
      </main>
    </div>
  )
}
