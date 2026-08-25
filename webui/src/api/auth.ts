import { client } from './client'
import type { ApiResponse } from '../types'

// 管理会话 token 的本地存储键（X-Admin-Token 头随请求携带）
export const ADMIN_TOKEN_KEY = 'ng_admin_token'
export const ADMIN_USER_KEY = 'ng_admin_user'
export const ADMIN_PERMS_KEY = 'ng_admin_perms'

export interface LoginResult {
  token: string
  username: string
  expires_at: string
  tenant_id?: string
  permissions?: string[]
  is_super?: boolean
}

export function getAdminToken(): string {
  return localStorage.getItem(ADMIN_TOKEN_KEY) || ''
}

export function setAdminSession(
  token: string,
  username: string,
  permissions: string[] = [],
  isSuper = false,
  tenantId = ''
): void {
  localStorage.setItem(ADMIN_TOKEN_KEY, token)
  localStorage.setItem(ADMIN_USER_KEY, username)
  // 权限快照与超管标记用于菜单显隐；权限判定以后端为准，前端仅做展示裁剪
  localStorage.setItem(ADMIN_PERMS_KEY, JSON.stringify({ permissions, is_super: isSuper, tenant_id: tenantId }))
}

export function clearAdminSession(): void {
  localStorage.removeItem(ADMIN_TOKEN_KEY)
  localStorage.removeItem(ADMIN_USER_KEY)
  localStorage.removeItem(ADMIN_PERMS_KEY)
}

export function getAdminUsername(): string {
  return localStorage.getItem(ADMIN_USER_KEY) || ''
}

export interface AdminIdentity {
  permissions: string[]
  is_super: boolean
  tenant_id: string
}

export function getAdminIdentity(): AdminIdentity {
  try {
    const raw = localStorage.getItem(ADMIN_PERMS_KEY)
    if (raw) return JSON.parse(raw) as AdminIdentity
  } catch {
    // 解析失败按无权限处理
  }
  return { permissions: [], is_super: false, tenant_id: '' }
}

export function hasPerm(perm: string): boolean {
  const identity = getAdminIdentity()
  return identity.is_super || identity.permissions.includes(perm)
}

export async function login(username: string, password: string): Promise<LoginResult> {
  const resp = await client.post<ApiResponse<LoginResult>>('/auth/login', { username, password })
  return resp.data.data
}

export async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  await client.put<ApiResponse<unknown>>('/auth/password', {
    old_password: oldPassword,
    new_password: newPassword
  })
}
