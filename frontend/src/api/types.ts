/** 与 internal/api 的 JSON 契约一一对应的类型定义。 */

export interface User {
  id: number
  nickname: string
  stars: number
  cleared: number
  top: number
  three: number
  fish?: Record<string, number>
  endless: number
  sudoku_best: number
  sudoku_stars?: Record<string, number>
  match_best: number
  items?: Record<string, number>
  reset_at: number
  skin: string
}

export interface QRInfo {
  id: string
  image: string
  expires_in: number
}

export type QRStatusName =
  | 'pending'
  | 'scanned'
  | 'authorized'
  | 'confirmed'
  | 'expired'
  | 'cancelled'
  | 'error'
  | 'unknown'

/** 扫码后展示的微信公开资料（不含任何凭证）。 */
export interface Account {
  openid: string
  nickname: string
  avatar?: string
}

/** 扫码成功时服务端一次性下发的登录态；Token 由前端写入本机 Cookie。 */
export interface LoginSession {
  token: string
  account: Account
  user?: User | null
}

export interface QRStatus {
  status: QRStatusName
  session?: LoginSession
  message?: string
}

export interface Modes {
  fish?: Record<string, number>
  endless: number
  sudoku_best: number
  sudoku_stars?: Record<string, number>
  match_best: number
}

export interface MeResponse {
  user: User | null
  modes: Modes
  items: Record<string, number>
}

export type JobStatus = 'running' | 'done' | 'error'

export interface Job {
  id?: string
  status: JobStatus
  phase: string
  cur: number
  total: number
  logs: string[]
  result?: Record<string, unknown>
  error?: string
  updated_at?: number
}

export interface AppInfo {
  app_id: string
  version: string
}

export type ActionKey =
  | 'clear'
  | 'sudoku'
  | 'fish'
  | 'endless'
  | 'match'
  | 'items'
  | 'adlog'
  | 'rank'
  | 'config'
