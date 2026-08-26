import { client } from './client'
import type { ApiResponse, Paged, ReportItem } from '../types'

// ===== 报表分页列表（period_start 倒序）=====
export async function listComplianceReports(params: {
  page: number
  size: number
}): Promise<Paged<ReportItem>> {
  const resp = await client.get<ApiResponse<Paged<ReportItem>>>('/compliance-reports', { params })
  return resp.data.data
}

// ===== 手动补生成（period_type 必填；start 为空取当前周期起点）=====
export async function generateComplianceReport(body: {
  period_type: 'day' | 'week' | 'month'
  start?: string // 2006-01-02
}): Promise<ReportItem> {
  const resp = await client.post<ApiResponse<ReportItem>>('/compliance-reports/generate', body)
  return resp.data.data
}

// ===== 产物下载：认证走 X-Admin-Token 头，window.open 无法携带，
// 故以 blob 拉取后自建 a 标签导出；文件名优先取 Content-Disposition =====
export async function downloadComplianceReport(id: string, format: 'json' | 'csv'): Promise<void> {
  const resp = await client.get(`/compliance-reports/${id}`, {
    params: { format },
    responseType: 'blob'
  })
  const disposition = String(resp.headers['content-disposition'] || '')
  const m = disposition.match(/filename="?([^";]+)"?/)
  const filename = m ? m[1] : `report-${id}.${format}`
  const url = URL.createObjectURL(resp.data as Blob)
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    link.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
