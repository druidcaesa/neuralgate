import { client } from './client'
import type { ApiKeyCreateRequest, ApiKeyCreateResult, ApiKeyItem, ApiResponse, Paged } from '../types'

export async function listApiKeys(page = 1, size = 10): Promise<Paged<ApiKeyItem>> {
  const resp = await client.get<ApiResponse<Paged<ApiKeyItem>>>('/api-keys', { params: { page, size } })
  return resp.data.data
}

export async function createApiKey(data: ApiKeyCreateRequest): Promise<ApiKeyCreateResult> {
  const resp = await client.post<ApiResponse<ApiKeyCreateResult>>('/api-keys', data)
  return resp.data.data
}

export async function updateApiKeyStatus(id: string, status: string): Promise<{ id: string }> {
  const resp = await client.patch<ApiResponse<{ id: string }>>(`/api-keys/${id}`, { status })
  return resp.data.data
}

export async function deleteApiKey(id: string): Promise<{ id: string }> {
  const resp = await client.delete<ApiResponse<{ id: string }>>(`/api-keys/${id}`)
  return resp.data.data
}
