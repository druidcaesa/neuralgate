import { client } from './client'
import type { ApiResponse, TamperAlertItem, TamperAlertQueryParams } from '../types'

export interface TamperAlertList {
  items: TamperAlertItem[]
  total: number
  page: number
  size: number
}

export async function listTamperAlerts(params: TamperAlertQueryParams): Promise<TamperAlertList> {
  const resp = await client.get<ApiResponse<TamperAlertList>>('/tamper-alerts', { params })
  return resp.data.data
}

export async function resolveTamperAlert(id: string, resolved: boolean): Promise<void> {
  await client.patch<ApiResponse<unknown>>(`/tamper-alerts/${id}`, { resolved })
}
