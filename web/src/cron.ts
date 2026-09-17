// Cron in words, for the common shapes; anything else is shown as the expression itself.

const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const macros: Record<string, string> = {
  '@yearly': '0 0 1 1 *', '@annually': '0 0 1 1 *', '@monthly': '0 0 1 * *', '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *', '@midnight': '0 0 * * *', '@hourly': '0 * * * *',
}
const num = (s: string) => (/^\d+$/.test(s) ? Number(s) : NaN)
const pad = (n: number) => String(n).padStart(2, '0')

/** "0 6 * * 1-5", "Europe/London" → "Weekdays at 06:00 (Europe/London)". */
export function describeCron(expr: string, timezone = 'UTC'): string {
  const fields = (macros[expr.trim()] ?? expr).trim().split(/\s+/)
  if (fields.length !== 5) return expr
  const [min, hour, dom, month, dow] = fields
  const zone = ` (${timezone})`
  const step = /^\*\/(\d+)$/.exec(min)
  if (step && hour === '*' && dom === '*' && month === '*' && dow === '*') return `Every ${step[1]} minutes`
  if (min === '*' && hour === '*' && dom === '*' && month === '*' && dow === '*') return 'Every minute'
  const m = num(min)
  if (!Number.isNaN(m) && hour === '*' && dom === '*' && month === '*' && dow === '*') return m === 0 ? 'Every hour, on the hour' : `Every hour at :${pad(m)}`
  const h = num(hour)
  if (Number.isNaN(m) || Number.isNaN(h) || month !== '*') return expr
  const at = `at ${pad(h)}:${pad(m)}${zone}`
  if (dom === '*' && dow === '*') return `Every day ${at}`
  if (dom === '*' && (dow === '1-5' || dow === 'MON-FRI')) return `Weekdays ${at}`
  if (dom === '*' && (dow === '0,6' || dow === '6,0' || dow === 'SAT,SUN')) return `Weekends ${at}`
  if (dom === '*' && /^[0-7]$/.test(dow)) return `Every ${days[Number(dow) % 7]} ${at}`
  if (dow === '*' && !Number.isNaN(num(dom))) return `On day ${dom} of every month ${at}`
  return expr
}
