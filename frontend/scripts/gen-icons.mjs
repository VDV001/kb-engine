// Собирает src/components/icons.ts из @material-symbols/svg-400.
//
// Запуск: npm run gen:icons
//
// Почему пути, а не шрифт: material-symbols-outlined.woff2 весит 3.8 МБ, а
// бинарь встраивает фронт целиком через go:embed. Полсотни путей — около 20 КБ,
// и в бандл попадают только те, что импортированы.
//
// Список ниже ведётся руками: иконка появляется здесь тогда, когда её начинают
// использовать. Автоматически вытаскивать имена из JSX смысла нет — их и так
// видно по ошибке типов, если имени в ICONS не окажется.
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'

const NAMES = [
  'account_balance_wallet', 'analytics', 'arrow_back', 'arrow_forward', 'arrow_outward',
  'balance', 'category', 'checkroom', 'chevron_left', 'chevron_right', 'cleaning_services',
  'code', 'construction', 'credit_card', 'dark_mode', 'dashboard', 'date_range', 'devices',
  'directions_car', 'download', 'filter_list', 'filter_list_off', 'fitness_center', 'flight',
  'health_and_safety', 'light_mode', 'history', 'home', 'hub', 'keyboard_arrow_down', 'keyboard_arrow_up',
  'close', 'lock', 'mail', 'menu', 'menu_book', 'mobile', 'more_horiz', 'open_in_new', 'payments', 'person',
  'point_of_sale', 'precision_manufacturing', 'psychology', 'receipt_long', 'redeem',
  'restaurant', 'school', 'search', 'search_off', 'send', 'settings', 'smart_toy', 'spa',
  'sports_esports', 'subscriptions', 'swap_horiz', 'trending_up', 'unfold_more', 'update',
  'verified_user', 'visibility', 'visibility_off',
]

const SRC = 'node_modules/@material-symbols/svg-400/outlined'
const OUT = 'src/components/icons.ts'

const entries = []
const missing = []
for (const name of [...NAMES].sort()) {
  const file = join(SRC, `${name}.svg`)
  if (!existsSync(file)) {
    missing.push(name)
    continue
  }
  const svg = readFileSync(file, 'utf8')
  const d = [...svg.matchAll(/<path d="([^"]+)"/g)].map((m) => m[1]).join(' ')
  if (!d) {
    missing.push(name)
    continue
  }
  entries.push(`  ${name}: ${JSON.stringify(d)},`)
}

if (missing.length > 0) {
  // Падаем, а не пропускаем молча: отсутствующее имя означает опечатку или
  // переименование в апстриме, и тихо выпавшая иконка обнаружится в проде.
  console.error(`✘ нет таких иконок в ${SRC}: ${missing.join(', ')}`)
  process.exit(1)
}

const header = `// Пути иконок Material Symbols (Apache-2.0) — только те, что реально нужны.
//
// СГЕНЕРИРОВАНО скриптом scripts/gen-icons.mjs (npm run gen:icons).
// Правится повторной генерацией, а не руками.
//
// Шрифт целиком весит 3.8 МБ, и вшивать его в бинарь ради полусотни глифов
// незачем — пути занимают около 20 КБ, причём в бандл попадают только те,
// что импортированы.

export const ICON_VIEWBOX = '0 -960 960 960'

export const ICONS = {
`

writeFileSync(OUT, `${header}${entries.join('\n')}\n} as const\n\nexport type IconName = keyof typeof ICONS\n`, 'utf8')
console.log(`✓ ${OUT}: ${entries.length} иконок`)
