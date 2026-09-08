import { client } from './client'
import type { ApiResponse, LicenseDetail, SystemInfo } from '../types'

// GatewayMeta 代理服务公开地址元信息(顶栏「接口说明」入口用);port 0 或空 scheme 视为未配置
export interface GatewayMeta {
  scheme: string
  port: number
  docs_path: string
}

// getGatewayMeta 取代理服务公开 scheme/端口,前端用 location.hostname 拼 http(s)://host:port/docs
export async function getGatewayMeta(): Promise<GatewayMeta> {
  const resp = await client.get<ApiResponse<GatewayMeta>>('/gateway-meta')
  return resp.data.data
}

export async function getSystemInfo(): Promise<SystemInfo> {
  const resp = await client.get<ApiResponse<SystemInfo>>('/system')
  return resp.data.data
}

export async function getLicense(): Promise<LicenseDetail> {
  const resp = await client.get<ApiResponse<LicenseDetail>>('/license')
  return resp.data.data
}

// uploadLicense 上传授权文件原文(JSON);成功后功能需重启生效
export async function uploadLicense(raw: string): Promise<{ message: string; customer_name: string }> {
  const resp = await client.post<ApiResponse<{ message: string; customer_name: string }>>(
    '/license',
    raw,
    { headers: { 'Content-Type': 'application/json' } }
  )
  return resp.data.data
}
