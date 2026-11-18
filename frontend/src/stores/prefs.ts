import { reactive, watch } from 'vue'

export type Theme = 'light' | 'dark'

export interface Prefs {
  theme: Theme
  clearFrom: number
  clearTo: number
  matchBest: number
  endlessFloor: number
  adCount: number
  rankType: string
}

const PREFS_KEY = 'sgz.prefs'
const THEME_KEY = 'sgz.theme'

const defaults: Prefs = {
  theme: 'light',
  clearFrom: 1,
  clearTo: 220,
  matchBest: 200,
  endlessFloor: 200,
  adCount: 12,
  rankType: 'stars'
}

function num(value: unknown, fallback: number): number {
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) && n > 0 ? Math.trunc(n) : fallback
}

function load(): Prefs {
  const base = { ...defaults }
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<Prefs>
      base.clearFrom = num(parsed.clearFrom, defaults.clearFrom)
      base.clearTo = num(parsed.clearTo, defaults.clearTo)
      base.matchBest = num(parsed.matchBest, defaults.matchBest)
      base.endlessFloor = num(parsed.endlessFloor, defaults.endlessFloor)
      base.adCount = num(parsed.adCount, defaults.adCount)
      if (typeof parsed.rankType === 'string' && parsed.rankType) base.rankType = parsed.rankType
    }
    base.theme = localStorage.getItem(THEME_KEY) === 'dark' ? 'dark' : 'light'
  } catch {
    // 忽略损坏的本地数据，回退默认值
  }
  return base
}

export const prefs = reactive<Prefs>(load())

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', theme === 'dark' ? '#0b0b0c' : '#fafafa')
  try {
    localStorage.setItem(THEME_KEY, theme)
  } catch {
    // 隐私模式下可能不可写
  }
}

export function setTheme(theme: Theme): void {
  prefs.theme = theme
  applyTheme(theme)
}

export function toggleTheme(): void {
  setTheme(prefs.theme === 'dark' ? 'light' : 'dark')
}

function save(): void {
  try {
    localStorage.setItem(
      PREFS_KEY,
      JSON.stringify({
        clearFrom: prefs.clearFrom,
        clearTo: prefs.clearTo,
        matchBest: prefs.matchBest,
        endlessFloor: prefs.endlessFloor,
        adCount: prefs.adCount,
        rankType: prefs.rankType
      })
    )
  } catch {
    // 隐私模式下可能不可写
  }
}

watch(
  () => [prefs.clearFrom, prefs.clearTo, prefs.matchBest, prefs.endlessFloor, prefs.adCount, prefs.rankType],
  save
)

applyTheme(prefs.theme)
