import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './styles/tokens.css' // 必须在 EP 样式之后引入，令 --el-* 覆盖生效
import App from './App.vue'
import router from './router'
import { initTheme } from './composables/useTheme'

initTheme()
const app = createApp(App)
app.use(ElementPlus, { locale: zhCn })
app.use(router)
app.mount('#app')
