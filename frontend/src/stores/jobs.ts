import { computed, reactive } from 'vue'
import { api, ApiError } from '@/api/client'
import type { ActionKey, Job } from '@/api/types'
import { errText, toast } from './ui'
import { clearLogin, session } from './session'

const HISTORY_KEY = 'sgz.jobHistory'
const MAX_HISTORY = 20
const MAX_LOGS = 800

export const ACTION_LABELS: Record<ActionKey, string> = {
  clear: '一键通关',
  sudoku: '数独喵',
  fish: '摸鱼 / 小鱼',
  endless: '无尽模式',
  match: '消消喵',
  items: '道具补满',
  adlog: '广告记录',
  rank: '排行榜',
  config: '配置查询'
}

export interface HistoryItem {
  id: string
  action: ActionKey
  label: string
  ref: string
  params: Record<string, unknown>
  startedAt: number
  finishedAt: number | null
  status: 'running' | 'done' | 'error'
  summary: string
}

function loadHistory(): HistoryItem[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((it): it is HistoryItem => typeof it === 'object' && it !== null && typeof (it as HistoryItem).id === 'string')
      .slice(0, MAX_HISTORY)
      .map((it) => ({ ...it, status: it.status === 'running' ? 'error' : it.status, summary: it.status === 'running' ? '已中断（页面刷新）' : it.summary }))
  } catch {
    return []
  }
}

function persistHistory(): void {
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(jobs.history.slice(0, MAX_HISTORY)))
  } catch {
    // 忽略
  }
}

export const jobs = reactive({
  job: null as Job | null,
  jobId: '',
  startedAt: 0,
  failures: 0,
  history: loadHistory()
})

let timer: number | null = null

export const jobRunning = computed(() => jobs.job?.status === 'running' && jobs.jobId !== '')

export function jobElapsedMs(): number {
  if (!jobs.startedAt) return 0
  const end = jobs.job?.updated_at && jobs.job.status !== 'running' ? jobs.job.updated_at : Date.now()
  return Math.max(0, end - jobs.startedAt)
}

function schedulePoll(delay: number): void {
  if (timer !== null) window.clearTimeout(timer)
  timer = window.setTimeout(() => void poll(), delay)
}

function findHistory(id: string): HistoryItem | undefined {
  return jobs.history.find((it) => it.id === id)
}

function summarize(job: Job): string {
  if (job.status === 'error') return job.error || '执行失败'
  const result = job.result ?? {}
  const keys = Object.keys(result)
  if (!keys.length) return '已完成'
  return keys
    .slice(0, 3)
    .map((key) => {
      const value = result[key]
      if (Array.isArray(value)) return `${key}[${value.length}]`
      if (value !== null && typeof value === 'object') return `${key}{…}`
      return `${key}=${String(value)}`
    })
    .join(' · ')
}

function finalize(job: Job): void {
  const item = findHistory(jobs.jobId)
  if (item) {
    item.status = job.status === 'error' ? 'error' : 'done'
    item.finishedAt = Date.now()
    item.summary = summarize(job)
    persistHistory()
  }
  if (job.status === 'error') toast(job.error || '任务执行失败', 'error')
  else toast(`任务完成：${item?.label ?? ''}`)
}

function failLocally(message: string): void {
  jobs.job = { status: 'error', phase: '已中止', cur: 0, total: 0, logs: [], error: message }
  const item = findHistory(jobs.jobId)
  if (item) {
    item.status = 'error'
    item.finishedAt = Date.now()
    item.summary = message
    persistHistory()
  }
  toast(message, 'error')
}

async function poll(): Promise<void> {
  if (!jobs.jobId) return
  const id = jobs.jobId
  try {
    if (!session.token) {
      failLocally('登录态已失效，请重新扫码')
      return
    }
    const job = await api.job(id, session.token)
    jobs.failures = 0
    if (job.logs.length > MAX_LOGS) job.logs = job.logs.slice(-MAX_LOGS)
    jobs.job = { ...job, id }
    if (job.status === 'running') {
      const item = findHistory(id)
      if (item) {
        item.status = 'running'
        item.summary = job.phase || '运行中'
        persistHistory()
      }
      schedulePoll(1000)
      return
    }
    finalize(job)
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      clearLogin()
      failLocally('登录态已失效，请重新扫码')
      return
    }
    if (err instanceof ApiError && err.status === 404) {
      failLocally('任务已失效（服务端可能已重启）')
      return
    }
    jobs.failures += 1
    if (jobs.failures <= 5) {
      schedulePoll(2000)
      return
    }
    failLocally(`任务状态读取失败：${errText(err)}`)
  }
}

export async function startJob(action: ActionKey, params: Record<string, unknown>): Promise<boolean> {
  if (!session.token) {
    toast('请先扫码登录', 'error')
    return false
  }
  if (jobRunning.value) {
    toast('已有任务在执行中，请等待完成', 'error')
    return false
  }
  try {
    const { job: id } = await api.action(session.token, action, params)
    jobs.jobId = id
    jobs.startedAt = Date.now()
    jobs.failures = 0
    jobs.job = { id, status: 'running', phase: '已创建，等待执行', cur: 0, total: 0, logs: [] }
    const item: HistoryItem = {
      id,
      action,
      label: ACTION_LABELS[action],
      ref: '',
      params,
      startedAt: jobs.startedAt,
      finishedAt: null,
      status: 'running',
      summary: '运行中…'
    }
    jobs.history = [item, ...jobs.history.filter((it) => it.id !== id)].slice(0, MAX_HISTORY)
    persistHistory()
    schedulePoll(300)
    toast(`已创建任务：${ACTION_LABELS[action]}`)
    return true
  } catch (err) {
    toast(`启动失败：${errText(err)}`, 'error')
    return false
  }
}

export function clearHistory(): void {
  jobs.history = []
  persistHistory()
}

export function stopPolling(): void {
  if (timer !== null) {
    window.clearTimeout(timer)
    timer = null
  }
}
