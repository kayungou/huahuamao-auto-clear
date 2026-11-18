import type {
  ActionKey,
  AppInfo,
  Job,
  MeResponse,
  QRInfo,
  QRStatus
} from './types'

/** ApiError 保留 HTTP 状态码与后端 {"error":"..."} 文案。 */
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown, token?: string): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin' }
  const headers: Record<string, string> = {}
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  if (token) headers.Authorization = `Bearer ${token}`
  if (Object.keys(headers).length > 0) init.headers = headers

  let res: Response
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError(0, '无法连接本地服务，请确认程序仍在运行')
  }
  const text = await res.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text) as unknown
    } catch {
      data = null
    }
  }
  if (!res.ok) {
    const msg =
      (typeof data === 'object' && data !== null && typeof (data as { error?: unknown }).error === 'string'
        ? (data as { error: string }).error
        : '') || `请求失败（HTTP ${res.status}）`
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export const api = {
  health: () => request<{ ok: boolean }>('GET', '/api/health'),
  appInfo: () => request<AppInfo>('GET', '/api/app'),

  newQR: () => request<QRInfo>('POST', '/api/qr/new'),
  pollQR: (id: string) => request<QRStatus>('GET', `/api/qr/poll?id=${encodeURIComponent(id)}`),

  me: (token: string) => request<MeResponse>('GET', '/api/game/me', undefined, token),
  action: (token: string, action: ActionKey, params: Record<string, unknown>) =>
    request<{ job: string }>('POST', '/api/game/action', { action, params }, token),

  job: (id: string, token: string) =>
    request<Job>('GET', `/api/jobs/${encodeURIComponent(id)}`, undefined, token)
}
