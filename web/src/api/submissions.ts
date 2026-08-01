import { api } from './client'
import type { Submission } from './types'

export const submissionsApi = {
  list: (limit = 20) =>
    api
      .get<{ submissions: Submission[] }>(`/api/v1/submissions?limit=${limit}`)
      .then((r) => r.data.submissions),
}
