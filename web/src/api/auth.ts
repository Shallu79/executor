import { api } from './client'
import type {
  Credentials,
  SigninResponse,
  SignupResponse,
  User,
} from './types'

// Session token attaches automatically via the axios interceptor.
export const authApi = {
  signup: (c: Credentials) =>
    api.post<SignupResponse>('/api/v1/auth/signup', c).then((r) => r.data),
  signin: (c: Credentials) =>
    api.post<SigninResponse>('/api/v1/auth/signin', c).then((r) => r.data),
  me: () => api.get<User>('/api/v1/me').then((r) => r.data),
}
