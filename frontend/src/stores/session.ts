import { computed, reactive } from 'vue'
import { api, ApiError } from '@/api/client'
import type { Account, AppInfo, LoginSession, MeResponse } from '@/api/types'
import { errText, toast } from './ui'

/**
 * 登录态只保存在本机 Cookie 里：
 *   sgz_session = { token, account, savedAt }
 * 服务端不保存任何账号、Token 或微信会话，因此刷新页面/重启服务都不影响已登录状态。
 */
const COOKIE_NAME = 'sgz_session'
const COOKIE_DAYS = 180

export interface StoredSession {
  token: string
  account: Account
  savedAt: number
}

export type Health = 'unknown' | 'ok' | 'fail'

function readCookie(): StoredSession | null {
  if (typeof document === 'undefined') return null
  const prefix = `${COOKIE_NAME}=`
  const raw = document.cookie
    .split('; ')
    .find((part) => part.startsWith(prefix))
    ?.slice(prefix.length)
  if (!raw) return null
  try {
    const parsed = JSON.parse(decodeURIComponent(raw)) as unknown
    if (typeof parsed !== 'object' || parsed === null) return null
    const obj = parsed as Record<string, unknown>
    if (typeof obj.token !== 'string' || !obj.token) return null
    const account = obj.account as Record<string, unknown> | undefined
    if (typeof account !== 'object' || account === null) return null
    if (typeof account.openid !== 'string') return null
    return {
      token: obj.token,
      account: {
        openid: account.openid,
        nickname: typeof account.nickname === 'string' ? account.nickname : '',
        avatar: typeof account.avatar === 'string' ? account.avatar : undefined
      },
      savedAt: typeof obj.savedAt === 'number' ? obj.savedAt : Date.now()
    }
  } catch {
    return null
  }
}

function writeCookie(value: StoredSession): void {
  const payload = encodeURIComponent(JSON.stringify(value))
  document.cookie = `${COOKIE_NAME}=${payload}; Path=/; Max-Age=${COOKIE_DAYS * 24 * 3600}; SameSite=Lax`
}

function clearCookie(): void {
  document.cookie = `${COOKIE_NAME}=; Path=/; Max-Age=0; SameSite=Lax`
}

const stored = readCookie()

export const session = reactive({
  health: 'unknown' as Health,
  appInfo: null as AppInfo | null,
  token: stored?.token ?? '',
  account: (stored?.account ?? null) as Account | null,
  me: null as MeResponse | null,
  loadingMe: false,
  checkingHealth: false
})

export const loggedIn = computed(() => Boolean(session.token))
export const hasMe = computed(() => session.me !== null)
export const currentAccount = computed<Account | null>(() => session.account)

export async function checkHealth(quiet = false): Promise<void> {
  if (session.checkingHealth) return
  session.checkingHealth = true
  try {
    await api.health()
    session.health = 'ok'
  } catch {
    session.health = 'fail'
    if (!quiet) toast('本地服务连接失败，请确认程序仍在运行', 'error')
  } finally {
    session.checkingHealth = false
  }
}

export function startHealthPolling(intervalMs = 20000): () => void {
  void checkHealth(true)
  const timer = window.setInterval(() => void checkHealth(true), intervalMs)
  return () => window.clearInterval(timer)
}

export async function loadAppInfo(): Promise<void> {
  try {
    session.appInfo = await api.appInfo()
  } catch {
    session.appInfo = null
  }
}

/** 登录态已失效（Token 被服务端拒绝）时清理本机 Cookie。 */
export function clearLogin(quiet = false): void {
  session.token = ''
  session.account = null
  session.me = null
  clearCookie()
  if (!quiet) toast('登录态已失效，请重新扫码', 'error')
}

export async function loadMe(quiet = false): Promise<void> {
  if (!session.token) {
    session.me = null
    return
  }
  session.loadingMe = true
  try {
    session.me = await api.me(session.token)
  } catch (err) {
    session.me = null
    if (err instanceof ApiError && (err.status === 401 || err.status === 404)) {
      clearLogin(quiet)
      return
    }
    if (!quiet) toast(`读取账号数据失败：${errText(err)}`, 'error')
  } finally {
    session.loadingMe = false
  }
}

export async function refreshMe(): Promise<void> {
  if (!session.token) {
    toast('请先扫码登录', 'error')
    return
  }
  await loadMe(false)
  if (session.me) toast('账号数据已刷新')
}

/** 扫码成功后把一次性 Token 与会话资料写入本机 Cookie。 */
export async function saveLogin(login: LoginSession): Promise<void> {
  const value: StoredSession = {
    token: login.token,
    account: login.account,
    savedAt: Date.now()
  }
  writeCookie(value)
  session.token = value.token
  session.account = value.account
  session.me = null
  await loadMe(true)
}

export function logout(): void {
  clearLogin(true)
  toast('已在本机退出登录')
}

export async function initSession(): Promise<void> {
  await loadAppInfo()
  if (session.token) await loadMe(true)
  else session.me = null
}

export { ApiError }
