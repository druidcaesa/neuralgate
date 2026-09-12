// dashboard JSON 契约护栏。
//
// pkg/admin/dashboard_test.go 的 jsonKey 表钉住了服务端实际下发的字段路径，
// webui/src/types/index.ts 是前端的消费声明。二者分处 Go 与 TS，
// 单边改名不会让任何一方的检查失败——直到线上取到 undefined。
// 本脚本把两边接起来：把 Go 表里的每条 path 当作属性链，
// 在 TS 接口中逐段解析，断链即失败。
//
// 用法：node scripts/check-dashboard-contract.mjs（由 make test-webui 调用）

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// 服务端统一响应包装 {code, message, data}，Data 段的类型名
const ROOT_TYPE = 'DashboardData'
const ENVELOPE = 'data'

const here = dirname(fileURLToPath(import.meta.url))
const GO_TEST = join(here, '..', '..', 'pkg', 'admin', 'dashboard_test.go')
const TS_TYPES = join(here, '..', 'src', 'types', 'index.ts')

// 解析 TS 接口声明：只认扁平的 export interface，字段行形如 `name?: Type`
function parseInterfaces(src) {
  const byName = new Map()
  let current = null
  for (const raw of src.split('\n')) {
    const line = raw.trim()
    if (!current) {
      const m = /^export interface (\w+) \{/.exec(line)
      if (m) current = { name: m[1], fields: new Map() }
      continue
    }
    if (line === '}') {
      byName.set(current.name, current)
      current = null
      continue
    }
    const f = /^(\w+)\??:\s*(.+?)\s*$/.exec(line)
    if (f) current.fields.set(f[1], f[2])
  }
  return byName
}

// 把一条 path 当作属性链在接口间走一遍；任一段落空即返回原因
function resolvePath(path, ifaces) {
  const segs = path.split('.')
  if (segs[0] !== ENVELOPE) {
    return { ok: false, reason: `不以 ${ENVELOPE}. 开头，护栏未覆盖该形态` }
  }
  if (segs.length < 2) return { ok: false, reason: '未指向包装内的任何字段' }

  let cur = ROOT_TYPE
  for (let i = 1; i < segs.length; i++) {
    const isLast = i === segs.length - 1
    let seg = segs[i]

    const bracket = seg.indexOf('[')
    if (bracket >= 0) {
      if (seg.slice(bracket) !== '[0]') {
        return { ok: false, reason: `只支持 [0] 下标，实得 ${seg.slice(bracket)}` }
      }
      seg = seg.slice(0, bracket)
    }

    const iface = ifaces.get(cur)
    if (!iface) {
      return { ok: false, reason: `${cur} 不是已声明的接口，字段 ${seg} 处断链` }
    }
    const declared = iface.fields.get(seg)
    if (declared === undefined) {
      return { ok: false, reason: `${cur} 未声明属性 ${seg}` }
    }

    const isArray = declared.endsWith('[]')
    const elem = isArray ? declared.slice(0, -2).trim() : declared

    if (bracket >= 0 && !isArray) {
      return { ok: false, reason: `${cur}.${seg} 声明为 ${declared}，非数组却按 [0] 取元素` }
    }
    if (isLast) return { ok: true }

    if (isArray && bracket < 0) {
      return { ok: false, reason: `${cur}.${seg} 是数组 ${declared}，取其元素字段须显式写 [0]` }
    }
    if (!ifaces.has(elem)) {
      return { ok: false, reason: `${cur}.${seg} 的后继类型 ${elem} 不是已声明的接口，无法继续解析` }
    }
    cur = elem
  }
  return { ok: false, reason: '空路径' }
}

const goSrc = readFileSync(GO_TEST, 'utf8')
const paths = []
for (const m of goSrc.matchAll(/\{\s*path:\s*"([^"]+)"/g)) {
  if (!paths.includes(m[1])) paths.push(m[1])
}

// 抽不到 path 说明 Go 表的写法变了或文件被移走——此时放行等于没有护栏
if (paths.length === 0) {
  console.error(`✗ 未能从 ${GO_TEST} 抽到任何 path，护栏已失效，中止`)
  process.exit(1)
}

const ifaces = parseInterfaces(readFileSync(TS_TYPES, 'utf8'))
if (!ifaces.has(ROOT_TYPE)) {
  console.error(`✗ ${TS_TYPES} 中找不到接口 ${ROOT_TYPE}，护栏已失效，中止`)
  process.exit(1)
}

const failures = []
for (const path of paths) {
  const r = resolvePath(path, ifaces)
  if (!r.ok) failures.push(`  ${path}\n    ${r.reason}`)
}

if (failures.length > 0) {
  console.error(`✗ dashboard 契约失配：${failures.length}/${paths.length} 条 path 无法在 TS 侧解析`)
  console.error(failures.join('\n'))
  console.error('\n改字段名时须同步 pkg/admin/dashboard_test.go 与 webui/src/types/index.ts')
  process.exit(1)
}

console.log(`✓ dashboard 契约一致：${paths.length} 条 path 全部在 types/index.ts 中解析成功`)
