// Load test for the Minos exec engine API.
//
// Run: k6 run scripts/k6-loadtest.js
// Env:  API_BASE=http://127.0.0.1:8080 (default)
//       PEAK_VUS=20                (max VUs for the load ramp, default 20)
//
// Scenarios:
//   load   - POST /api/v1/execute (the sync execution path) with a
//            ramping VU load: 0 -> 5 -> 10 -> 20 VUs.
//   auth   - read-only account traffic (/me, /api-keys, /submissions).
import http from 'k6/http'
import { check, sleep } from 'k6'
import { Counter, Rate, Trend } from 'k6/metrics'

const BASE = __ENV.API_BASE || 'http://127.0.0.1:8080'
const PASSWORD = 'loadtest-password'
const PEAK = Number(__ENV.PEAK_VUS || 20)
const JOB =
  __ENV.JOB_JSON ||
  JSON.stringify({ language: 'python', code: 'print("hello from k6 load test")' })

export const options = {
  scenarios: {
    load: {
      executor: 'ramping-vus',
      exec: 'execute',
      startVUs: 0,
      stages: [
        { duration: '20s', target: Math.round(PEAK / 4) },
        { duration: '40s', target: Math.round(PEAK / 2) },
        { duration: '40s', target: PEAK },
        { duration: '60s', target: PEAK },
        { duration: '20s', target: 0 },
      ],
      gracefulStop: '10s',
    },
    auth: {
      executor: 'constant-vus',
      exec: 'account',
      vus: 3,
      duration: '60s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{scenario:load}': ['p(95)<8000'],
    'http_req_duration{scenario:auth}': ['p(95)<500'],
    completed_rate: ['rate>0.90'],
  },
}

const completed = new Counter('completed')
const processing = new Counter('processing')
const completedRate = new Rate('completed_rate')
const execMs = new Trend('exec_time_ms')

export function setup() {
  const email = `load-${Date.now()}@example.com`
  const r = http.post(
    `${BASE}/api/v1/auth/signup`,
    JSON.stringify({ email, password: PASSWORD }),
    { headers: { 'Content-Type': 'application/json' } },
  )
  check(r, { 'signup 201': (res) => res.status === 201 })
  const signin = http.post(
    `${BASE}/api/v1/auth/signin`,
    JSON.stringify({ email, password: PASSWORD }),
    { headers: { 'Content-Type': 'application/json' } },
  )
  check(signin, { 'signin 200': (res) => res.status === 200 })
  return { apiKey: r.json().api_key, token: signin.json().token }
}

export function execute(data) {
  const headers = { 'Content-Type': 'application/json', 'X-API-Key': data.apiKey }
  const res = http.post(`${BASE}/api/v1/execute`, JOB, { headers, timeout: '35s' })

  const ok = res.status === 200
  if (ok) {
    const b = res.json()
    const done = b.status === 'completed'
    completed.add(1)
    completedRate.add(done)
    if (done) execMs.add(b.time_ms || 0)
  } else if (res.status === 504) {
    processing.add(1)
    completedRate.add(false)
  } else {
    completedRate.add(false)
  }

  check(res, {
    'execute http 200': (r) => r.status === 200,
    'execute completed': (r) => r.status === 200 && r.json().status === 'completed',
  })
  sleep(0.2)
}

export function account(data) {
  const headers = { Authorization: `Bearer ${data.token}` }
  const me = http.get(`${BASE}/api/v1/me`, { headers })
  const keys = http.get(`${BASE}/api/v1/api-keys`, { headers })
  const subs = http.get(`${BASE}/api/v1/submissions?limit=20`, { headers })
  check(me, { 'me 200': (r) => r.status === 200 })
  check(keys, { 'api-keys 200': (r) => r.status === 200 })
  check(subs, { 'submissions 200': (r) => r.status === 200 })
  sleep(0.5)
}
