import { watch } from 'vue'
import { useTheme } from './useTheme'

// ChartPalette 图表色板，取值来自 --ng-* 设计令牌。
// 需要按序列取色时统一按 primary → success → warning → danger → info 顺次取用
export interface ChartPalette {
  primary: string
  success: string
  warning: string
  danger: string
  info: string
  textSecondary: string
  border: string
  card: string
}

// readVar 读取 <html> 上生效的 CSS 变量值
function readVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

// readChartPalette 当前主题下的色板。
// echarts 无法直接消费 CSS 变量，必须在绘制前取值写入 option；
// 暗色下若沿用 echarts 默认色，坐标轴与分割线在深色卡底上几乎不可见
export function readChartPalette(): ChartPalette {
  return {
    primary: readVar('--ng-primary'),
    success: readVar('--ng-success'),
    warning: readVar('--ng-warning'),
    danger: readVar('--ng-danger'),
    info: readVar('--ng-info'),
    textSecondary: readVar('--ng-text-secondary'),
    border: readVar('--ng-border'),
    card: readVar('--ng-bg-card')
  }
}

// useChartTheme 在明暗切换后回调 onThemeChange（此时 useTheme 已同步改写
// html 的 dark 类，重新读取即可拿到新令牌），返回色板读取函数
export function useChartTheme(onThemeChange: () => void) {
  const { theme } = useTheme()
  watch(theme, onThemeChange)
  return { palette: readChartPalette }
}
