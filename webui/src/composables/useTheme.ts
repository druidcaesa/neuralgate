import { ref } from 'vue'

export type ThemeMode = 'light' | 'dark'

const STORAGE_KEY = 'ng-theme'
const theme = ref<ThemeMode>('light')

// 把主题应用到 <html>：data-theme 供 --ng-* 覆盖，dark 类供 Element Plus 暗色接管
function apply(mode: ThemeMode) {
  const el = document.documentElement
  el.dataset.theme = mode
  el.classList.toggle('dark', mode === 'dark')
}

// 启动时：已保存偏好优先，否则跟随系统 prefers-color-scheme
export function initTheme() {
  const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null
  const prefersDark = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
  theme.value = saved ?? (prefersDark ? 'dark' : 'light')
  apply(theme.value)
}

export function useTheme() {
  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem(STORAGE_KEY, theme.value)
    apply(theme.value)
  }
  return { theme, toggle }
}
