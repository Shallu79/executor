import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { keysApi } from '../api'
import type { APIKey } from '../api'
import { Badge, Button, CopyButton, Modal, Spinner, inputCls, timeAgo } from './ui'

export default function KeysSection() {
  const [items, setItems] = useState<APIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [name, setName] = useState('')
  const [createBusy, setCreateBusy] = useState(false)
  const [newKey, setNewKey] = useState<string | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    setLoading(true)
    keysApi
      .list()
      .then(setItems)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(load, [load])

  async function create(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setCreateBusy(true)
    setError('')
    try {
      const r = await keysApi.create(name.trim())
      setNewKey(r.key)
      setName('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed')
    } finally {
      setCreateBusy(false)
    }
  }

  async function revoke(k: APIKey, revoked: boolean) {
    await keysApi.setRevoked(k.id, revoked)
    await load()
  }

  async function remove(k: APIKey) {
    if (!confirm(`Delete API key "${k.name}"? This cannot be undone.`)) return
    await keysApi.remove(k.id)
    await load()
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-bold text-ink">API Keys</h1>
        <p className="mt-1 text-sm text-muted">
          Credentials for submitting code to the exec engine.
        </p>
      </header>

      <form onSubmit={create} className="flex max-w-md gap-2">
        <input
          className={inputCls}
          placeholder="Key name, e.g. ci / local / prod"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button type="submit" disabled={createBusy || !name.trim()}>
          {createBusy ? <Spinner /> : 'Create'}
        </Button>
      </form>

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
          No API keys yet — create one above.
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((k) => (
            <div
              key={k.id}
              className="flex flex-wrap items-center gap-3 rounded-xl border border-edge bg-panel p-4"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold text-ink">{k.name}</span>
                  {k.revoked ? (
                    <Badge tone="danger">Revoked</Badge>
                  ) : (
                    <Badge tone="ok">Active</Badge>
                  )}
                </div>
                <code className="mt-1 block font-mono text-xs text-muted">
                  {k.prefix}••••••••••••••••••••
                </code>
                <div className="mt-1 text-[11px] text-faint">
                  created {timeAgo(k.created_at)} · last used {timeAgo(k.last_used_at)}
                </div>
              </div>
              <div className="flex items-center gap-2">
                {k.revoked ? (
                  <Button variant="ghost" className="!py-1.5 text-xs" onClick={() => revoke(k, false)}>
                    Restore
                  </Button>
                ) : (
                  <Button variant="ghost" className="!py-1.5 text-xs" onClick={() => revoke(k, true)}>
                    Revoke
                  </Button>
                )}
                <Button variant="danger" className="!py-1.5 text-xs" onClick={() => remove(k)}>
                  Delete
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      <Modal open={newKey !== null} onClose={() => setNewKey(null)} title="API key created">
        <p className="mb-3 text-sm text-muted">
          Your key is shown once. Store it securely — the server only keeps a hash.
        </p>
        <div className="flex items-center gap-2 rounded-lg border border-brand/30 bg-surface p-3">
          <code className="flex-1 break-all font-mono text-xs text-brand">{newKey}</code>
          {newKey && <CopyButton value={newKey} />}
        </div>
      </Modal>
    </div>
  )
}
