import { useEffect, useMemo, useState } from 'react'
import { Group, Panel, Separator } from 'react-resizable-panels'
import CodeMirror from '@uiw/react-codemirror'
import { oneDark } from '@codemirror/theme-one-dark'
import { python } from '@codemirror/lang-python'
import { javascript } from '@codemirror/lang-javascript'
import { cpp } from '@codemirror/lang-cpp'
import { java } from '@codemirror/lang-java'
import type { Extension } from '@codemirror/state'

import { executionApi, getSessionToken } from '../api'
import type { JobResponse } from '../api'
import { getRunnerKey, onRunnerKeyChange, setRunnerKey } from '../lib/runnerKey'
import { useIsMobile } from '../lib/useIsMobile'
import { Badge, Button, Dropdown, Field, Spinner, inputCls } from './ui'
import TopBar from './TopBar'

const LANGS = [
  { id: 'python', label: 'Python', sample: 'print("hello from python")\nfor i in range(3):\n    print(i)' },
  { id: 'javascript', label: 'JavaScript', sample: 'console.log("hello from node");\nconst x = 6 * 7;\nconsole.log(x);' },
  { id: 'go', label: 'Go', sample: 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello from go")\n}' },
  { id: 'c', label: 'C', sample: '#include <stdio.h>\n\nint main(void) {\n    printf("hello from c\\n");\n    return 0;\n}' },
  { id: 'cpp', label: 'C++', sample: '#include <iostream>\n\nint main() {\n    std::cout << "hello from c++\\n";\n    return 0;\n}' },
  { id: 'java', label: 'Java', sample: 'public class Main {\n    public static void main(String[] args) {\n        System.out.println("hello from java");\n    }\n}' },
  { id: 'bash', label: 'Bash', sample: 'echo "hello from bash"' },
]

// CodeMirror language extensions for the languages that have one.
const LANG_EXT: Record<string, Extension> = {
  python: python(),
  javascript: javascript(),
  c: cpp(),
  cpp: cpp(),
  java: java(),
}

export default function IdePage() {
  const [lang, setLang] = useState(LANGS[0])
  const [code, setCode] = useState(LANGS[0].sample)
  const [stdin, setStdin] = useState('')
  const [apiKey, setApiKey] = useState(() => getRunnerKey())
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<JobResponse | null>(null)
  const [error, setError] = useState('')
  const isMobile = useIsMobile()

  // Keep the key in sync with anything else that writes it (Get API key flow).
  useEffect(() => onRunnerKeyChange(setApiKey), [])

  const extension = useMemo(() => (LANG_EXT[lang.id] ? [LANG_EXT[lang.id]] : []), [lang.id])

  function pickLang(id: string) {
    const l = LANGS.find((x) => x.id === id) ?? LANGS[0]
    setLang(l)
    setCode(l.sample)
  }

  async function run() {
    setBusy(true)
    setError('')
    setResult(null)
    try {
      let res = await executionApi.run(apiKey, { language: lang.id, code, stdin: stdin || undefined })
      if (res.status === 'processing' && res.job_id) {
        for (let i = 0; i < 30; i++) {
          await new Promise((r) => setTimeout(r, 1000))
          res = await executionApi.poll(apiKey, res.job_id)
          if (res.status === 'completed') break
        }
      }
      setResult(res)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  const runButton = (
    <Button
      onClick={run}
      disabled={busy || !apiKey}
      className="shrink-0 rounded-full !px-4"
      title={apiKey ? 'Run' : 'Paste an API key first'}
    >
      {busy ? (
        <Spinner />
      ) : (
        <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor">
          <path d="M8 5v14l11-7z" />
        </svg>
      )}
      {busy ? 'Running…' : 'Run'}
    </Button>
  )

  const apiKeyField = (
    <div className="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-edge bg-panel-2 px-3 py-1.5 md:w-56 md:flex-none">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="shrink-0 text-muted">
        <circle cx="8" cy="15" r="4" />
        <path d="M10.9 12.1 20 3M15 8l3 3M17 6l2 2" />
      </svg>
      <input
        className="w-full min-w-0 bg-transparent font-mono text-xs text-ink placeholder:text-faint outline-none"
        type="password"
        placeholder={getSessionToken() ? 'API key (minos_)' : 'Paste an API key'}
        value={apiKey}
        onChange={(e) => {
          setApiKey(e.target.value)
          setRunnerKey(e.target.value)
        }}
      />
      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${apiKey ? 'bg-emerald-400' : 'bg-faint'}`} />
    </div>
  )

  return (
    <div className="flex h-screen flex-col overflow-hidden">
      <TopBar
        center={<div className="hidden md:block">{runButton}</div>}
      >
        <div className="hidden items-center gap-2 md:flex">
          <Dropdown
            className="shrink-0 !w-40"
            value={lang.id}
            onChange={pickLang}
            options={LANGS.map((l) => ({ value: l.id, label: l.label }))}
          />
          {apiKeyField}
        </div>
      </TopBar>

      {/* Mobile toolbar */}
      <div className="flex items-center gap-2 border-b border-edge bg-panel-2 px-3 py-2 md:hidden">
        <Dropdown
          className="shrink-0 !w-32"
          value={lang.id}
          onChange={pickLang}
          options={LANGS.map((l) => ({ value: l.id, label: l.label }))}
        />
        {apiKeyField}
        {runButton}
      </div>

      {/* Editor + console, edge-to-edge and resizable */}
      <div className="min-h-0 flex-1">
        <Group orientation={isMobile ? 'vertical' : 'horizontal'} className="h-full">
          <Panel className="overflow-hidden bg-panel">
            <CodeMirror
              value={code}
              onChange={setCode}
              height="100%"
              theme={oneDark}
              extensions={extension}
              basicSetup={{ highlightActiveLineGutter: true, foldGutter: true }}
              style={{ height: '100%', fontSize: 13 }}
            />
          </Panel>

          <Separator
            className={`shrink-0 bg-edge transition-colors hover:bg-brand/40 ${
              isMobile ? 'h-1 cursor-row-resize' : 'w-1 cursor-col-resize'
            }`}
          />

          <Panel className="flex min-h-0 flex-col bg-panel">
            <div className="flex shrink-0 items-center justify-between border-b border-edge bg-panel-2 px-3 py-1.5 text-[11px] uppercase tracking-wider text-faint">
              <span>Console</span>
              {result && (
                <span className="flex items-center gap-2 normal-case tracking-normal">
                  <Badge tone={result.exit_code === 0 ? 'ok' : 'danger'}>exit {result.exit_code ?? '?'}</Badge>
                  <Badge tone="neutral">{result.time_ms ?? '?'} ms</Badge>
                  {result.truncated && <Badge tone="warn">truncated</Badge>}
                </span>
              )}
            </div>

            {error && (
              <div className="shrink-0 border-b border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">{error}</div>
            )}

            <div className="grid min-h-0 flex-1 grid-rows-2 gap-px bg-edge">
              <ConsolePane label="stdout" value={result?.stdout ?? ''} tone="text-emerald-300" />
              <ConsolePane label="stderr" value={result?.stderr ?? ''} tone="text-rose-300" />
            </div>

            <div className="shrink-0 border-t border-edge px-3 py-2">
              <Field label="stdin">
                <textarea
                  className={inputCls + ' min-h-16 resize-y font-mono'}
                  value={stdin}
                  onChange={(e) => setStdin(e.target.value)}
                  placeholder={'program input\none value per line'}
                  rows={2}
                />
              </Field>
            </div>
          </Panel>
        </Group>
      </div>
    </div>
  )
}

function ConsolePane({ label, value, tone }: { label: string; value: string; tone: string }) {
  return (
    <div className="min-h-0 overflow-hidden bg-panel">
      <div className="px-3 pt-1.5 text-[10px] uppercase tracking-wider text-faint">{label}</div>
      <pre
        className={`h-full overflow-auto whitespace-pre-wrap px-3 pb-2 font-mono text-xs leading-relaxed ${tone} ${
          value ? '' : 'text-faint italic'
        }`}
      >
        {value || '(empty)'}
      </pre>
    </div>
  )
}
