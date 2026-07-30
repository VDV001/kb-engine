import { useEffect, useState } from 'react'
import { api } from './api'
import type { Analytics, AnalyticsConfig, Audits, DuplicateGroup, Entry, Finances, Stats } from './api'
import { AnalyticsView } from './AnalyticsView'
import { CatalogView } from './CatalogView'
import { Header } from './components/Header'
import { ErrorBox, Spinner } from './components/ui'
import {
  AuditsView,
  DuplicatesView,
  FinancesView,
  OverviewView,
  SettingsView,
} from './views'

type Tab = 'overview' | 'archives' | 'analytics' | 'audits' | 'duplicates' | 'finances' | 'settings'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Dashboard' },
  { id: 'archives', label: 'Archives' },
  { id: 'analytics', label: 'Analytics' },
  { id: 'audits', label: 'Audits' },
  { id: 'duplicates', label: 'Duplicates' },
  { id: 'finances', label: 'Finances' },
  { id: 'settings', label: 'Summary' },
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
      <Header tabs={tabs} current={tab} onSelect={setTab} count={data?.stats.total} />

      <main className="mx-auto max-w-screen-2xl px-4 py-8 sm:px-6 lg:px-8">
        {error && <ErrorBox message={error} />}
        {!error && !data && <Spinner />}
        {data && (
          <>
            {tab === 'overview' && <OverviewView stats={data.stats} />}

            {tab === 'analytics' && (
              <AnalyticsView config={data.analyticsConfig} stats={data.stats} />
            )}
            {tab === 'audits' && <AuditsView audits={data.audits} />}
            {tab === 'duplicates' && <DuplicatesView groups={data.duplicates} />}
            {tab === 'archives' && <CatalogView entries={data.entries} />}
            {tab === 'finances' && <FinancesView finances={data.finances} />}
            {tab === 'settings' && <SettingsView stats={data.stats} />}
          </>
        )}
      </main>
    </div>
  )
}
