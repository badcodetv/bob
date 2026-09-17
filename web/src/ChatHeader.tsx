import { useState } from 'react'
import { Button } from '@/components/ui/button'
import type { Settings, Worker } from './api'
import { claudeEfforts, claudeModels } from './engines/claude'

// Choices offered per engine. An empty value means "the worker's own setting".
const options: Record<string, { models: string[]; efforts: string[] }> = {
  claude: { models: claudeModels, efforts: claudeEfforts },
}

export function ChatHeader({ worker, engine, workerDefaults, settings, onChange, onDelete }: {
  worker: string
  engine: string
  workerDefaults?: Pick<Worker, 'model' | 'effort'>
  settings: Settings
  onChange: (s: Settings) => void
  onDelete?: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const opts = options[engine] ?? { models: [], efforts: [] }
  const select = (key: keyof Settings, values: string[], fallback?: string) => (
    <select
      className="rounded border bg-background px-2 py-1 text-xs"
      value={settings[key]}
      onChange={(e) => onChange({ ...settings, [key]: e.target.value })}
      title={key}
    >
      <option value="">{key}: {fallback ? `${fallback} (worker)` : 'default'}</option>
      {settings[key] && !values.includes(settings[key]) && <option value={settings[key]}>{settings[key]}</option>}
      {values.map((v) => <option key={v} value={v}>{v}</option>)}
    </select>
  )
  return (
    <div className="flex items-center gap-2 border-b px-4 py-2 text-sm">
      <span className="font-medium">{worker}</span>
      <span className="text-xs text-muted-foreground">{engine}</span>
      <div className="ml-auto flex items-center gap-2">
        {select('model', opts.models, workerDefaults?.model)}
        {select('effort', opts.efforts, workerDefaults?.effort)}
        {onDelete && (confirming
          ? <>
              <Button variant="destructive" size="sm" onClick={onDelete}>Delete chat</Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirming(false)}>Keep</Button>
            </>
          : <Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>Delete</Button>)}
      </div>
    </div>
  )
}
