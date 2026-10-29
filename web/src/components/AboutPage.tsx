import type { ReactNode } from 'react'
import TopBar from './TopBar'

export default function AboutPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <TopBar />
      <main className="mx-auto w-full max-w-3xl flex-1 px-4 py-8 md:px-8">
        <h1 className="font-pixel text-lg text-brand [text-shadow:2px_2px_0_rgba(0,0,0,0.9)]">
          EXECUTOR
        </h1>
        <p className="mt-2 text-sm text-muted">Secure, multi-language code execution.</p>

        <div className="mt-6 space-y-4 rounded-2xl border border-edge bg-panel p-6">
          <Section title="What is it?">
            Executor is a code execution engine with a sandboxed runner. Paste your code, pick a
            language, hit Run — the job is queued, executed in isolation, and returns stdout,
            stderr, exit code and timing.
          </Section>
          <Section title="Languages">
            Python, JavaScript/Node, Go, C, C++, Java and Bash are supported out of the box.
          </Section>
          <Section title="API keys">
            Create API keys from your profile so you (or CI pipelines) can submit jobs over HTTP.
          </Section>
        </div>
      </main>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h2 className="text-sm font-semibold text-ink">{title}</h2>
      <p className="mt-1 text-sm leading-relaxed text-muted">{children}</p>
    </div>
  )
}
