import { useEffect, useState } from 'react'
import { api } from './api'
import type { Analytics, AnalyticsConfig, Audits, DuplicateGroup, Entry, Finances, Stats } from './api'
import { ThemeToggle } from './components/ThemeToggle'
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
      // Finances read two files that are edited by hand while the dashboard is
      // open, so this request can fail on its own — while LibreOffice is saving,
      // for instance. That must not take the other six views down with it: the
      // finances view already renders an empty state.
      api.finances().catch(() => ({ transactions: [], accounts: [] })),
    ])
      .then(([stats, entries, audits, duplicates, analytics, analyticsConfig, finances]) =>
        setData({ stats, entries, audits, duplicates, analytics, analyticsConfig, finances }),
      )
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  return (
    <div className="min-h-screen bg-bg text-on-surface">
      <header className="flex items-center justify-between border-b border-outline-variant bg-surface-low px-6 py-4">
        <div>
          <h1 className="text-xl">kb-engine</h1>
          <p className="text-sm text-on-surface-variant">Дашборд базы знаний</p>
        </div>
        <ThemeToggle />
      </header>

      <nav className="flex gap-1 border-b border-outline-variant bg-surface-low px-6">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium ${
              tab === t.id
                ? 'border-secondary text-secondary'
                : 'border-transparent text-on-surface-variant hover:text-on-surface'
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
