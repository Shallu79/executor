import { api } from './client'
import type { JobRequest, JobResponse } from './types'

// Execution uses a user API key (X-API-Key), not the session token.
const withKey = (apiKey: string) => ({ 'X-API-Key': apiKey })

export const executionApi = {
  run: (apiKey: string, job: JobRequest) =>
    api.post<JobResponse>('/api/v1/execute', job, { headers: withKey(apiKey) }).then((r) => r.data),
  poll: (apiKey: string, jobId: string) =>
    api.get<JobResponse>(`/api/v1/jobs/${jobId}`, { headers: withKey(apiKey) }).then((r) => r.data),
}
