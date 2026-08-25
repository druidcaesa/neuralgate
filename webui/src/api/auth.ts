import { client } from './client'
import type { ApiResponse } from '../types'

// 管理会话 token 的本地存储键（X-Admin-Token 头随请求携带）
export const ADMIN_TOKEN_KEY = 'ng_admin_token'
export const ADMIN_USER_KEY = 'ng_admin_user'

export interface LoginResult {
  token: string
  username: string
  expires_at: string
}

export function getAdminToken(): string {
  return localStorage.getItem(ADMIN_TOKEN_KEY) || ''
}

export function setAdminSession(token: string, username: string): void {
  localStorage.setItem(ADMIN_TOKEN_KEY, token)
  localStorage.setItem(ADMIN_USER_KEY, username)
}

export function clearAdminSession(): void {
  localStorage.removeItem(ADMIN_TOKEN_KEY)
  localStorage.removeItem(ADMIN_USER_KEY)
}

export function getAdminUsername(): string {
  return localStorage.getItem(ADMIN_USER_KEY) || ''
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
