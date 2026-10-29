// Shared runner API key store. The key is remembered in localStorage and can
// be set from multiple places (topbar "Get API key", the IDE toolbar) — any
// subscriber (the IDE) is notified on change.

const KEY = 'executor_runner_key'

type Listener = (key: string) => void

const listeners = new Set<Listener>()

export function getRunnerKey(): string {
  return localStorage.getItem(KEY) ?? ''
}

export function setRunnerKey(key: string): void {
  localStorage.setItem(KEY, key)
  listeners.forEach((l) => l(key))
}

export function onRunnerKeyChange(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
