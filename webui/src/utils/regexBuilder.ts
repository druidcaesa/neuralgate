// 隐私合规页「可视化正则生成」的纯函数库。
//
// 生成的表达式必须兼容后端 Go 引擎的 RE2 语义：
//   - 不使用环视 / 反向引用（RE2 不支持），只用 \b \d 字符类与非捕获组 (?:)
//   - 替换是字面量（ReplaceAllLiteral，不解释 $1），因此各模板给出的是完整匹配 + 占位替换串
//   - Go 的 \b 只识别 ASCII 单词字符，中文前后不加 \b（否则永不命中），此处按内容自动判定

export type RuleKind = 'pii' | 'output' | 'injection' | 'whitelist'

export type TemplateId =
  | 'phone'
  | 'idcard'
  | 'bankcard'
  | 'email'
  | 'digits'
  | 'prefix-digits'
  | 'phrase'

export interface TemplateState {
  wholeWord: boolean
  ignoreCase: boolean
  allowSeparators: boolean
  looseWhitespace: boolean
  prefix: string
  phrase: string
  minLen: number
  maxLen: number
}

export type OptionKey = keyof TemplateState

export interface OptionField {
  key: OptionKey
  kind: 'switch' | 'text' | 'minmax'
  label: string
  placeholder?: string
  hint?: string
  when?: (s: TemplateState) => boolean
}

export interface TemplateDef {
  id: TemplateId
  label: string
  appliesTo: RuleKind[]
  /** pii/output 命中后自动预填的替换占位串 */
  defaultReplacement: string
  /** 当前模板的默认选项（覆盖通用默认） */
  defaults: Partial<TemplateState>
  options: OptionField[]
  build(s: TemplateState): string | null
}

/** 转义正则元字符，用户输入一律按字面嵌入 */
export function escapeLiteral(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function defaultTemplateState(): TemplateState {
  return {
    wholeWord: true,
    ignoreCase: false,
    allowSeparators: false,
    looseWhitespace: false,
    prefix: '',
    phrase: '',
    minLen: 6,
    maxLen: 12,
  }
}

export function freshState(def: TemplateDef): TemplateState {
  return { ...defaultTemplateState(), ...def.defaults }
}

function clampLen(n: number): number {
  const v = Math.floor(Number.isFinite(n) ? n : 6)
  return Math.min(99, Math.max(1, v))
}

/** \d{n} 或 \d{n,m}（n==m 时省略上限） */
function digitRange(s: TemplateState): string {
  const lo = clampLen(s.minLen)
  const hi = clampLen(s.maxLen)
  const min = Math.min(lo, hi)
  const max = Math.max(lo, hi)
  return min === max ? `\\d{${min}}` : `\\d{${min},${max}}`
}

const isAsciiWordChar = (c: string): boolean => /[A-Za-z0-9_]/.test(c)

/** 按需在首尾加 \b：仅当该侧字符是 ASCII 单词字符时才成立（中文等不加） */
function wrapWord(core: string, leadWord: boolean, tailWord: boolean): string {
  return `${leadWord ? '\\b' : ''}${core}${tailWord ? '\\b' : ''}`
}

function buildPhone(s: TemplateState): string {
  const core = s.allowSeparators ? `1[3-9](?:[ -]?\\d){9}` : `1[3-9]\\d{9}`
  return wrapWord(core, s.wholeWord, s.wholeWord)
}

function buildIdcard(s: TemplateState): string {
  return wrapWord(`\\d{17}[\\dXx]`, s.wholeWord, s.wholeWord)
}

function buildBankcard(s: TemplateState): string {
  return wrapWord(`\\d{16,19}`, s.wholeWord, s.wholeWord)
}

function buildEmail(): string {
  // 邮箱本身带字母/数字与 @、.，加 \b 会破坏结构，模板固定无词边界
  return `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}`
}

function buildDigits(s: TemplateState): string {
  return wrapWord(digitRange(s), s.wholeWord, s.wholeWord)
}

function buildPrefixDigits(s: TemplateState): string | null {
  const prefix = (s.prefix || '').trim()
  if (!prefix) return null
  const leadW = s.wholeWord && isAsciiWordChar(prefix[0])
  const tailW = s.wholeWord // 末尾是数字，必然为单词字符
  const cs = s.ignoreCase ? '(?i)' : ''
  const core = `${escapeLiteral(prefix)}${digitRange(s)}`
  return cs + wrapWord(core, leadW, tailW)
}

const CJK = /[一-鿿]/

function buildPhrase(s: TemplateState): string | null {
  const phrase = (s.phrase || '').trim()
  if (!phrase) return null
  const cs = s.ignoreCase ? '(?i)' : ''
  const hasCJK = CJK.test(phrase)
  let body: string
  if (s.looseWhitespace) {
    if (hasCJK) {
      // 中文：相邻汉字之间允许任意空白（抗 LLM 输出在字间插空格）
      const chars = Array.from(phrase)
      body = chars
        .map((c, i) => {
          const next = chars[i + 1]
          const loose = next !== undefined && CJK.test(c) && CJK.test(next)
          return `${escapeLiteral(c)}${loose ? '\\s*' : ''}`
        })
        .join('')
    } else {
      // ASCII：词与词之间按 \s+ 归一（原始空格/换行均可）
      body = phrase
        .split(/\s+/)
        .filter(Boolean)
        .map(escapeLiteral)
        .join('\\s+')
    }
  } else {
    body = escapeLiteral(phrase)
  }
  // 词边界只对"单个 ASCII 单词"（无空格、无中文）有意义
  const singleAsciiWord = /^[A-Za-z0-9_]+$/.test(phrase)
  const lead = s.wholeWord && singleAsciiWord ? '\\b' : ''
  const tail = lead
  return `${cs}${lead}${body}${tail}`
}

export const templateCatalog: TemplateDef[] = [
  {
    id: 'phone',
    label: '手机号（大陆）',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '1**********',
    defaults: {},
    options: [
      { key: 'wholeWord', kind: 'switch', label: '整词边界', hint: '避免匹配长数字串中间的片段' },
      { key: 'allowSeparators', kind: 'switch', label: '允许空格/横线分隔', hint: '如 138-1234-5678 也能命中' },
    ],
    build: buildPhone,
  },
  {
    id: 'idcard',
    label: '身份证号（18 位）',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '******************',
    defaults: {},
    options: [{ key: 'wholeWord', kind: 'switch', label: '整词边界' }],
    build: buildIdcard,
  },
  {
    id: 'bankcard',
    label: '银行卡号',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '**** **** **** ****',
    defaults: {},
    options: [{ key: 'wholeWord', kind: 'switch', label: '整词边界' }],
    build: buildBankcard,
  },
  {
    id: 'email',
    label: '邮箱',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '***@***.***',
    defaults: {},
    options: [],
    build: () => buildEmail(),
  },
  {
    id: 'digits',
    label: '任意数字串（自定义位数）',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '[已隐藏]',
    defaults: { minLen: 6, maxLen: 12 },
    options: [
      { key: 'minLen', kind: 'minmax', label: '位数范围' },
      { key: 'wholeWord', kind: 'switch', label: '整词边界' },
    ],
    build: buildDigits,
  },
  {
    id: 'prefix-digits',
    label: '前缀 + 数字（编号 / 工号 / 单号）',
    appliesTo: ['pii', 'output'],
    defaultReplacement: '[已隐藏]',
    defaults: { ignoreCase: true, minLen: 4, maxLen: 8 },
    options: [
      { key: 'prefix', kind: 'text', label: '编号前缀', placeholder: '如 EMP / JD' },
      { key: 'minLen', kind: 'minmax', label: '数字位数范围' },
      { key: 'ignoreCase', kind: 'switch', label: '忽略大小写' },
      { key: 'wholeWord', kind: 'switch', label: '整词边界' },
    ],
    build: buildPrefixDigits,
  },
  {
    id: 'phrase',
    label: '文本短语（字面匹配）',
    appliesTo: ['pii', 'output', 'injection', 'whitelist'],
    defaultReplacement: '[已隐藏]',
    defaults: { wholeWord: false },
    options: [
      {
        key: 'phrase',
        kind: 'text',
        label: '要匹配的内容',
        placeholder: '如：忽略以上所有指令 / ignore previous instructions',
      },
      { key: 'ignoreCase', kind: 'switch', label: '忽略大小写（含英文时）' },
      {
        key: 'looseWhitespace',
        kind: 'switch',
        label: '字词间允许空白',
        hint: '中文逐字、英文按词拆分，可抗空格/换行干扰',
      },
      {
        key: 'wholeWord',
        kind: 'switch',
        label: '整词边界',
        when: (s) => /^[A-Za-z0-9_]+$/.test((s.phrase || '').trim()),
      },
    ],
    build: buildPhrase,
  },
]

export function templateById(id: TemplateId | ''): TemplateDef | undefined {
  return templateCatalog.find((t) => t.id === id)
}

export function templatesFor(kind: RuleKind): TemplateDef[] {
  return templateCatalog.filter((t) => t.appliesTo.includes(kind))
}

/** 供样例自测调用：返回命中次数与替换预览（前端演示，与引擎语义一致则结果一致） */
export function previewMask(text: string, pattern: string, replacement: string): { count: number; preview: string } {
  let re: RegExp
  try {
    re = new RegExp(pattern, 'g')
  } catch {
    return { count: -1, preview: '' }
  }
  const parts = text.split(re)
  const count = parts.length - 1
  // 字面量替换：用 split/join 代替 String.replace，避免 replacement 里的 $ 被当作分组引用；
  // replacement 为空字符串表示命中即删除
  const masked = parts.join(replacement)
  return { count, preview: masked }
}
