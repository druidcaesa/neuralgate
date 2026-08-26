import { client } from './client'
import type { ApiResponse, Paged, MCPServerItem, MCPAuditLogItem } from '../types'

// ===== MCP 上游配置 CRUD =====
export async function listMCPServers(params: { page: number; size: number }): Promise<Paged<MCPServerItem>> {
  const resp = await client.get<ApiResponse<Paged<MCPServerItem>>>('/mcp-servers', { params })
  return resp.data.data
}

export interface MCPServerPayload {
  name: string
  endpoint: string
  headers?: Record<string, string>
  enabled?: boolean
}

export async function createMCPServer(body: MCPServerPayload): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/mcp-servers', body)
  return resp.data.data
}

export async function updateMCPServer(id: string, body: MCPServerPayload): Promise<{ id: string }> {
  const resp = await client.put<ApiResponse<{ id: string }>>(`/mcp-servers/${id}`, body)
  return resp.data.data
}

export async function deleteMCPServer(id: string): Promise<void> {
  await client.delete(`/mcp-servers/${id}`)
}

// ===== 工具调用审计查询 =====
export interface MCPAuditQuery {
  page: number
  size: number
  tool?: string
  status?: string
  request_id?: string
  start?: string // RFC3339 或 YYYY-MM-DD
  end?: string
}

export async function listMCPAuditLogs(params: MCPAuditQuery): Promise<Paged<MCPAuditLogItem>> {
  const resp = await client.get<ApiResponse<Paged<MCPAuditLogItem>>>('/mcp-audit-logs', { params })
  return resp.data.data
}

export async function getMCPAuditLog(id: string): Promise<MCPAuditLogItem> {
  const resp = await client.get<ApiResponse<MCPAuditLogItem>>(`/mcp-audit-logs/${id}`)
  return resp.data.data
}
