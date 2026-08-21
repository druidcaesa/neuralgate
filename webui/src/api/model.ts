import { client } from './client'
import type { ApiResponse, ModelCreateRequest, ModelItem, Paged, UpstreamItem, UpstreamRequest } from '../types'

export async function listModels(page = 1, size = 10): Promise<Paged<ModelItem>> {
  const resp = await client.get<ApiResponse<Paged<ModelItem>>>('/models', { params: { page, size } })
  return resp.data.data
}

export async function createModel(data: ModelCreateRequest): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/models', data)
  return resp.data.data
}

export async function updateModel(id: string, data: ModelCreateRequest): Promise<{ id: string }> {
  const resp = await client.put<ApiResponse<{ id: string }>>(`/models/${id}`, data)
  return resp.data.data
}

export async function deleteModel(id: string): Promise<{ id: string }> {
  const resp = await client.delete<ApiResponse<{ id: string }>>(`/models/${id}`)
  return resp.data.data
}

export async function testModel(id: string): Promise<{ ok: boolean; latency_ms: number; error?: string; status?: number }> {
  const resp = await client.post<ApiResponse<{ ok: boolean; latency_ms: number; error?: string; status?: number }>>(`/models/${id}/test`)
  return resp.data.data
}

export async function listUpstreams(modelID: string): Promise<UpstreamItem[]> {
  const resp = await client.get<ApiResponse<{ items: UpstreamItem[]; total: number }>>(`/models/${modelID}/upstreams`)
  return resp.data.data.items
}

export async function createUpstream(modelID: string, data: UpstreamRequest): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>(`/models/${modelID}/upstreams`, data)
  return resp.data.data
}

export async function updateUpstream(uid: string, data: UpstreamRequest): Promise<{ id: string }> {
  const resp = await client.put<ApiResponse<{ id: string }>>(`/upstreams/${uid}`, data)
  return resp.data.data
}

export async function deleteUpstream(uid: string): Promise<{ id: string }> {
  const resp = await client.delete<ApiResponse<{ id: string }>>(`/upstreams/${uid}`)
  return resp.data.data
}
