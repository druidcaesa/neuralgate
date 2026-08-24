import { client } from './client'
import type { ApiResponse, LicenseDetail, SystemInfo } from '../types'

export async function getSystemInfo(): Promise<SystemInfo> {
  const resp = await client.get<ApiResponse<SystemInfo>>('/system')
  return resp.data.data
}

export async function getLicense(): Promise<LicenseDetail> {
  const resp = await client.get<ApiResponse<LicenseDetail>>('/license')
  return resp.data.data
}
