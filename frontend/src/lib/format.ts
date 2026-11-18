const intFormatter = new Intl.NumberFormat('zh-CN')

/** 千分位整数；undefined / NaN 显示 "—"。 */
export function fmtInt(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—'
  return intFormatter.format(Math.trunc(value))
}

/** 后端时间戳兼容秒/毫秒，统一转 Date。 */
export function toDate(ts: number | null | undefined): Date | null {
  if (!ts || ts <= 0) return null
  return new Date(ts < 1e12 ? ts * 1000 : ts)
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** 形如 11-12 09:31；今天只显示 09:31。 */
export function fmtTime(ts: number | null | undefined): string {
  const d = toDate(ts)
  if (!d) return '—'
  const now = new Date()
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return hm
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`
}

/** 精确到秒的时间，用于日志/任务。 */
export function fmtTimeFull(ts: number | null | undefined): string {
  const d = toDate(ts)
  if (!d) return '—'
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 毫秒 → "1.2s" / "3m04s" / "1h02m"。 */
export function fmtDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const total = Math.round(ms / 1000)
  if (total < 60) return `${(ms / 1000).toFixed(1)}s`
  const m = Math.floor(total / 60)
  const s = total % 60
  if (m < 60) return `${m}m${pad(s)}s`
  const h = Math.floor(m / 60)
  return `${h}h${pad(m % 60)}m`
}

/** 中间截断，保留头尾；用于 openid / token。 */
export function truncateMiddle(text: string, head = 8, tail = 6): string {
  if (!text) return '—'
  if (text.length <= head + tail + 1) return text
  return `${text.slice(0, head)}…${text.slice(-tail)}`
}

/** 首字符作为头像占位。 */
export function initial(text: string): string {
  const t = (text || '').trim()
  return t ? Array.from(t)[0]!.toUpperCase() : '?'
}

/** 对象数字键排序后转数组，用于小鱼 / 数独进度。 */
export function numericEntries(map: Record<string, number> | undefined): Array<[number, number]> {
  if (!map) return []
  return Object.keys(map)
    .map((k) => [Number(k), map[k] ?? 0] as [number, number])
    .filter(([k]) => Number.isFinite(k))
    .sort((a, b) => a[0] - b[0])
}
