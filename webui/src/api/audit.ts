import { client } from './client'
import type { ApiResponse, AuditDetail, AuditLogItem, AuditQueryParams, Paged } from '../types'

export async function listAuditLogs(params: AuditQueryParams): Promise<Paged<AuditLogItem>> {
  const resp = await client.get<ApiResponse<Paged<AuditLogItem>>>('/audit-logs', { params })
  return resp.data.data
}

export async function getAuditDetail(requestID: string): Promise<AuditDetail> {
  const resp = await client.get<ApiResponse<AuditDetail>>(`/audit-logs/${requestID}`)
  return resp.data.data
}

// 导出:用 window.open 触发下载(不经过 axios 拦截器)
export function auditExportURL(format: string, params: AuditQueryParams): string {
  const query = new URLSearchParams()
  query.set('format', format)
  if (params.keyword) query.set('keyword', params.keyword)
  return `/api/audit-logs/export?${query.toString()}`
}
