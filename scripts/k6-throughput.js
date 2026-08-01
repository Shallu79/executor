// High-throughput battle test for the Minos exec engine API.
//
// Uses ramping-arrival-rate to drive a fixed number of executions per second
// (500 -> 1000 -> 1500 by default). If the system can't keep up, k6 reports
// dropped_iterations and the sync execute endpoint starts returning 504s.
//
// Run: k6 run scripts/k6-throughput.js
// Env:  API_BASE  = http://127.0.0.1:8080  (default)
//       RATE      = 1500                    (peak iterations/sec, default 1000)
//       JOB_JSON  = json payload to execute (default: trivial python job)
import http from 'k6/http'
import { check } from 'k6'
import { Counter, Rate } from 'k6/metrics'

const BASE = __ENV.API_BASE || 'http://127.0.0.1:8080'
const PASSWORD = 'loadtest-password'
const PEAK_RATE = Number(__ENV.RATE || 1000)
const JOB =
  __ENV.JOB_JSON ||
  JSON.stringify({ language: 'python', code: 'print("hello from k6 throughput test")' })

export const options = {
  scenarios: {
    throughput: {
      executor: 'ramping-arrival-rate',
      exec: 'execute',
      startRate: Math.round(PEAK_RATE / 3),
      timeUnit: '1s',
      preAllocatedVUs: 600,
      maxVUs: 2500,
      stages: [
        { duration: '30s', target: Math.round((PEAK_RATE * 2) / 3) },
        { duration: '30s', target: PEAK_RATE },
        { duration: '60s', target: PEAK_RATE },
        { duration: '20s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
    dropped_iterations: ['rate<0.05'],
    'http_req_duration{scenario:throughput}': ['p(95)<10000'],
  },
}

const completed = new Counter('completed')
const processing = new Counter('processing')
const completedRate = new Rate('completed_rate')

export function setup() {
  const email = `tp-${Date.now()}@example.com`
  const r = http.post(
    `${BASE}/api/v1/auth/signup`,
    JSON.stringify({ email, password: PASSWORD }),
    { headers: { 'Content-Type': 'application/json' } },
  )
  check(r, { 'signup 201': (res) => res.status === 201 })
  return { apiKey: r.json().api_key }
}

export function execute(data) {
  const headers = { 'Content-Type': 'application/json', 'X-API-Key': data.apiKey }
  const res = http.post(`${BASE}/api/v1/execute`, JOB, { headers, timeout: '35s' })

  if (res.status === 200) {
    const done = res.json().status === 'completed'
    completed.add(1)
    completedRate.add(done)
    if (!done) processing.add(1)
  } else if (res.status === 504) {
    processing.add(1)
    completedRate.add(false)
  } else {
    completedRate.add(false)
  }

  check(res, {
    'execute completed': (r) => r.status === 200 && r.json().status === 'completed',
  })
}
