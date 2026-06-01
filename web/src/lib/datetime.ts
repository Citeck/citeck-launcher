// Shared date/time formatters for the UI. All output is dd.mm.yy and 24-hour,
// in the user's LOCAL timezone — deliberately locale-independent so the format
// never flips to mm/dd or 12h on a differently-configured system/webview.

function parse(input: string | number | Date | null | undefined): Date | null {
  if (input === null || input === undefined || input === '') return null
  const d = input instanceof Date ? input : new Date(input)
  return Number.isNaN(d.getTime()) ? null : d
}

const pad2 = (n: number) => String(n).padStart(2, '0')

/** dd.mm.yy — empty string for missing/invalid input. */
export function formatDate(input: string | number | Date | null | undefined): string {
  const d = parse(input)
  if (!d) return ''
  return `${pad2(d.getDate())}.${pad2(d.getMonth() + 1)}.${String(d.getFullYear()).slice(-2)}`
}

/** HH:mm:ss (24h) — empty string for missing/invalid input. */
export function formatTime(input: string | number | Date | null | undefined): string {
  const d = parse(input)
  if (!d) return ''
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`
}

/** dd.mm.yy HH:mm:ss (24h) — empty string for missing/invalid input. */
export function formatDateTime(input: string | number | Date | null | undefined): string {
  const d = parse(input)
  if (!d) return ''
  return `${formatDate(d)} ${formatTime(d)}`
}
