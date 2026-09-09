// 时间展示统一格式化工具：按本地时区输出 yyyy-MM-dd HH:mm:ss / yyyy-MM-dd。
// 工程无日期库，仅用原生 Date + 补零；解析失败时原样返回，避免吞掉「unknown」等非时间值。

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDate(value?: string | null): Date | null {
  if (!value) return null
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? null : d
}

/** 时间 → yyyy-MM-dd HH:mm:ss(本地时区)；空 → '-'；解析失败 → 原样返回 */
export function formatTime(value?: string | null): string {
  if (!value) return '-'
  const d = toDate(value)
  if (!d) return value
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 日期 → yyyy-MM-dd(本地时区)；已是 yyyy-MM-dd 日期串则原样返回(避免时区偏移挪天)；空 → '-'；解析失败 → 原样返回 */
export function formatDate(value?: string | null): string {
  if (!value) return '-'
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  const d = toDate(value)
  if (!d) return value
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
