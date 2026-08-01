import { api } from './client'
import type { APIKey, CreateKeyResponse } from './types'

export const keysApi = {
  list: () =>
    api.get<{ api_keys: APIKey[] }>('/api/v1/api-keys').then((r) => r.data.api_keys),
  create: (name: string) =>
    api
      .post<CreateKeyResponse>('/api/v1/api-keys', { name })
      .then((r) => r.data),
  setRevoked: (id: string, revoked: boolean) =>
    api.patch<{ revoked: boolean }>(`/api/v1/api-keys/${id}`, { revoked }).then((r) => r.data),
  remove: (id: string) => api.delete(`/api/v1/api-keys/${id}`),
}
