import { client } from './client'
import type { ApiResponse, DashboardData, DashboardWindow } from '../types'

// getDashboard 取首页聚合数据；窗口仅 24h|7d|30d，非法值服务端返回 400
export async function getDashboard(window: DashboardWindow): Promise<DashboardData> {
  const resp = await client.get<ApiResponse<DashboardData>>('/dashboard', { params: { window } })
  return resp.data.data
}
