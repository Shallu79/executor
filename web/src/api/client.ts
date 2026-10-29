import axios from 'axios'
import type { AxiosError } from 'axios'

const TOKEN_KEY = 'executor_session_token'
const NEW_KEY_KEY = 'executor_new_key'

// API base URL comes from the build-time VITE_API_BASE env var (empty =
// same-origin, e.g. served behind the nginx /api proxy). Falls back to the
// local dev API when the var is not set. Not user-configurable at runtime.
export function getBaseURL(): string {
  return import.meta.env.VITE_API_BASE === undefined
    ? 'http://127.0.0.1:8080'
    : import.meta.env.VITE_API_BASE
}

export function getSessionToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}
export function setSessionToken(token: string | null): void {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

// The newly-created key shown once on the Overview after signup.
export function getNewKey(): string | null {
  return localStorage.getItem(NEW_KEY_KEY)
}
export function setNewKey(key: string | null): void {
  if (key) localStorage.setItem(NEW_KEY_KEY, key)
  else localStorage.removeItem(NEW_KEY_KEY)
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// Single shared axios instance. baseURL follows the stored API base, and the
// request interceptor attaches the session token automatically — backend
// session is a stateless JWT we hold and forward on every request.
export const api = axios.create({ baseURL: getBaseURL() })

api.interceptors.request.use((config) => {
  const token = getSessionToken()
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

api.interceptors.response.use(
  (res) => res,
  (error: AxiosError<{ error?: string }>) => {
    const status = error.response?.status ?? 0
    const message =
      error.response?.data?.error ??
      (status ? `HTTP ${status}` : 'Network error')
    return Promise.reject(new ApiError(status, message))
  },
)
