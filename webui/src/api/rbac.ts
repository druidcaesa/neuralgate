import { client } from './client'
import type {
  ApiResponse,
  Paged,
  TenantItem,
  RoleItem,
  AdminUserItem,
  OperationLogItem
} from '../types'

// ===== 租户 =====
export async function listTenants(params: { page: number; size: number }): Promise<Paged<TenantItem>> {
  const resp = await client.get<ApiResponse<Paged<TenantItem>>>('/tenants', { params })
  return resp.data.data
}

export async function createTenant(tenant: TenantItem): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/tenants', tenant)
  return resp.data.data
}

export async function updateTenant(id: string, tenant: TenantItem): Promise<void> {
  await client.put(`/tenants/${id}`, tenant)
}

export async function deleteTenant(id: string): Promise<void> {
  await client.delete(`/tenants/${id}`)
}

// ===== 角色 =====
export async function listRoles(): Promise<RoleItem[]> {
  const resp = await client.get<ApiResponse<RoleItem[]>>('/roles')
  return resp.data.data ?? []
}

export async function createRole(role: RoleItem): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/roles', role)
  return resp.data.data
}

export async function updateRole(id: string, role: RoleItem): Promise<void> {
  await client.put(`/roles/${id}`, role)
}

export async function deleteRole(id: string): Promise<void> {
  await client.delete(`/roles/${id}`)
}

// ===== 用户 =====
export interface AdminUserList {
  items: AdminUserItem[]
}

export async function listAdminUsers(): Promise<AdminUserItem[]> {
  const resp = await client.get<ApiResponse<AdminUserList>>('/admin-users')
  return resp.data.data?.items ?? []
}

export async function createAdminUser(user: AdminUserItem & { password: string }): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/admin-users', user)
  return resp.data.data
}

export async function updateAdminUser(
  id: string,
  user: AdminUserItem & { password?: string }
): Promise<void> {
  await client.put(`/admin-users/${id}`, user)
}

export async function deleteAdminUser(id: string): Promise<void> {
  await client.delete(`/admin-users/${id}`)
}

// ===== 操作日志 =====
export async function listOperationLogs(params: {
  page: number
  size: number
  user_id?: string
}): Promise<Paged<OperationLogItem>> {
  const resp = await client.get<ApiResponse<Paged<OperationLogItem>>>('/operation-logs', { params })
  return resp.data.data
}
