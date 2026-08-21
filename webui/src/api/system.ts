import { client } from './client'
import type { ApiResponse, SystemInfo } from '../types'

export async function getSystemInfo(): Promise<SystemInfo> {
  const resp = await client.get<ApiResponse<SystemInfo>>('/system')
  return resp.data.data
}
