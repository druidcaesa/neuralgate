import axios from 'axios'
import { ElMessage } from 'element-plus'
import { ADMIN_TOKEN_KEY } from './auth'

export const client = axios.create({
  baseURL: '/api',
  timeout: 15000
})

// 请求拦截:携带管理会话 token(登录后所有 /api 需认证)
client.interceptors.request.use((config) => {
  const token = localStorage.getItem(ADMIN_TOKEN_KEY)
  if (token) {
    config.headers.set('X-Admin-Token', token)
  }
  return config
})

// 响应拦截:统一错误处理 + 解包 data；401 清会话回登录页
client.interceptors.response.use(
  (resp) => {
    const body = resp.data
    if (body && typeof body === 'object' && 'code' in body && body.code !== 0) {
      ElMessage.error(body.message || '操作失败')
      return Promise.reject(new Error(body.message))
    }
    return resp
  },
  (err) => {
    if (err.response?.status === 401 && window.location.pathname !== '/login') {
      localStorage.removeItem(ADMIN_TOKEN_KEY)
      ElMessage.error('登录已失效，请重新登录')
      window.location.href = '/login'
      return Promise.reject(err)
    }
    const msg =
      err.response?.data?.message ||
      err.response?.data?.error?.message ||
      (err.code === 'ECONNABORTED' ? '请求超时' : '无法连接管理后台')
    ElMessage.error(msg)
    return Promise.reject(err)
  }
)
