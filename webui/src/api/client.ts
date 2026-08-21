import axios from 'axios'
import { ElMessage } from 'element-plus'

export const client = axios.create({
  baseURL: '/api',
  timeout: 15000
})

// 响应拦截:统一错误处理 + 解包 data
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
    const msg =
      err.response?.data?.message ||
      err.response?.data?.error?.message ||
      (err.code === 'ECONNABORTED' ? '请求超时' : '无法连接管理后台')
    ElMessage.error(msg)
    return Promise.reject(err)
  }
)
