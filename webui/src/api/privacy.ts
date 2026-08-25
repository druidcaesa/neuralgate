import { client } from './client'
import type { ApiResponse, Paged, PrivacyRuleItem, PrivacyWhitelistItem, SecurityEventItem } from '../types'

// ===== 规则库 =====
export async function listPrivacyRules(ruleType?: string): Promise<PrivacyRuleItem[]> {
  const params = ruleType ? { rule_type: ruleType } : {}
  const resp = await client.get<ApiResponse<PrivacyRuleItem[]>>('/privacy-rules', { params })
  return resp.data.data ?? []
}

export async function createPrivacyRule(rule: PrivacyRuleItem): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/privacy-rules', rule)
  return resp.data.data
}

export async function updatePrivacyRule(id: string, rule: PrivacyRuleItem): Promise<void> {
  await client.put(`/privacy-rules/${id}`, rule)
}

export async function deletePrivacyRule(id: string): Promise<void> {
  await client.delete(`/privacy-rules/${id}`)
}

// ===== 白名单 =====
export async function listPrivacyWhitelist(): Promise<PrivacyWhitelistItem[]> {
  const resp = await client.get<ApiResponse<PrivacyWhitelistItem[]>>('/privacy-whitelist')
  return resp.data.data ?? []
}

export async function createPrivacyWhitelistEntry(entry: PrivacyWhitelistItem): Promise<{ id: string }> {
  const resp = await client.post<ApiResponse<{ id: string }>>('/privacy-whitelist', entry)
  return resp.data.data
}

export async function deletePrivacyWhitelistEntry(id: string): Promise<void> {
  await client.delete(`/privacy-whitelist/${id}`)
}

// ===== 安全事件 =====
export async function listSecurityEvents(params: { page: number; size: number }): Promise<Paged<SecurityEventItem>> {
  const resp = await client.get<ApiResponse<Paged<SecurityEventItem>>>('/security-events', { params })
  return resp.data.data
}
