import { client } from './client'
import type { ApiResponse, Paged, RateLimitItem, RateLimitRequest } from '../types'

export async function listRateLimits(page = 1, size = 10): Promise<Paged<RateLimitItem>> {
  const resp = await client.get<ApiResponse<Paged<RateLimitItem>>>('/rate-limits', { params: { page, size } })
  return resp.data.data
}

export async function createRateLimit(data: RateLimitRequest): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/rate-limits', data)
  return resp.data.data
}

export async function updateRateLimit(id: string, data: RateLimitRequest): Promise<{ id: string }> {
  const resp = await client.put<ApiResponse<{ id: string }>>(`/rate-limits/${id}`, data)
  return resp.data.data
}

export async function deleteRateLimit(id: string): Promise<{ id: string }> {
  const resp = await client.delete<ApiResponse<{ id: string }>>(`/rate-limits/${id}`)
  return resp.data.data
}
