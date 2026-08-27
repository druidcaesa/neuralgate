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

// uploadLicense 上传授权文件原文(JSON);成功后功能需重启生效
export async function uploadLicense(raw: string): Promise<{ message: string; customer_name: string }> {
  const resp = await client.post<ApiResponse<{ message: string; customer_name: string }>>(
    '/license',
    raw,
    { headers: { 'Content-Type': 'application/json' } }
  )
  return resp.data.data
}
