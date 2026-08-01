export type Credentials = { email: string; password: string }

export type User = { id: string; email: string; created_at: string }

export type APIKey = {
  id: string
  name: string
  prefix: string
  revoked: boolean
  last_used_at: string | null
  created_at: string
}

export type Submission = {
  id: string
  language: string
  code: string
  stdin: string
  status: 'queued' | 'completed'
  exit_code: number
  stdout: string
  stderr: string
  time_ms: number
  created_at: string
}

export type JobRequest = {
  language: string
  code: string
  stdin?: string
}

export type JobResponse = {
  job_id: string
  status: 'processing' | 'completed'
  stdout?: string
  stderr?: string
  exit_code?: number
  time_ms?: number
  truncated?: boolean
  error?: string
}

export type SignupResponse = { user: User; api_key: string }
export type SigninResponse = { token: string; user: User }
export type CreateKeyResponse = { api_key: APIKey; key: string }
