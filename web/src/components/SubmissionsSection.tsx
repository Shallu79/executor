import { useEffect, useState } from 'react'
import { submissionsApi } from '../api'
import type { Submission } from '../api'
import { Badge, Modal, Spinner, formatDate, timeAgo } from './ui'

export default function SubmissionsSection() {
  const [items, setItems] = useState<Submission[]>([])
  const [selected, setSelected] = useState<Submission | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    submissionsApi
      .list(50)
      .then(setItems)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-bold text-ink">Submissions</h1>
        <p className="mt-1 text-sm text-muted">Your job history, newest first.</p>
      </header>

      {error && (
        <p className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
          {error}
        </p>
      )}

      {loading ? (
        <div className="flex justify-center py-10 text-muted">
          <Spinner />
        </div>
      ) : items.length === 0 ? (
        <div className="rounded-xl border border-dashed border-edge py-10 text-center text-sm text-faint">
          No submissions yet. Run code from the Code Runner tab.
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-edge">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-edge bg-panel-2 text-[11px] uppercase tracking-wider text-faint">
                <th className="px-4 py-3 font-medium">Language</th>
                <th className="px-4 py-3 font-medium">Status</th>
                <th className="px-4 py-3 font-medium">Exit</th>
                <th className="px-4 py-3 font-medium">Time</th>
                <th className="px-4 py-3 font-medium">When</th>
              </tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <tr
                  key={s.id}
                  onClick={() => setSelected(s)}
                  className="cursor-pointer border-b border-edge/60 bg-panel transition-colors last:border-0 hover:bg-panel-2"
                >
                  <td className="px-4 py-3 font-mono text-xs text-brand">{s.language}</td>
                  <td className="px-4 py-3">
                    {s.status === 'completed' ? (
                      <Badge tone="ok">completed</Badge>
                    ) : (
                      <Badge tone="warn">queued</Badge>
                    )}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-ink">{s.exit_code}</td>
                  <td className="px-4 py-3 font-mono text-xs text-muted">{s.time_ms} ms</td>
                  <td className="px-4 py-3 text-xs text-muted">{timeAgo(s.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Modal open={selected !== null} onClose={() => setSelected(null)} title={selected ? `Submission · ${selected.language}` : ''}>
        {selected && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-2 text-xs">
              <Badge tone={selected.status === 'completed' ? 'ok' : 'warn'}>{selected.status}</Badge>
              <Badge tone="neutral">exit {selected.exit_code}</Badge>
              <Badge tone="neutral">{selected.time_ms} ms</Badge>
              <Badge tone="neutral">{formatDate(selected.created_at)}</Badge>
            </div>
            <Out label="stdout" value={selected.stdout} tone="text-emerald-300" />
            <Out label="stderr" value={selected.stderr} tone="text-rose-300" />
            <Out label="code" value={selected.code} tone="text-ink" />
          </div>
        )}
      </Modal>
    </div>
  )
}

function Out({ label, value, tone }: { label: string; value: string; tone: string }) {
  return (
    <div>
      <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">{label}</div>
      <pre
        className={`max-h-52 overflow-auto whitespace-pre-wrap rounded-lg border border-edge bg-surface p-3 font-mono text-xs leading-relaxed ${tone} ${
          value ? '' : 'text-faint italic'
        }`}
      >
        {value || '(empty)'}
      </pre>
    </div>
  )
}
