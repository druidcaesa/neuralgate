<template>
  <div class="rfp">
    <div class="rfp-row">
      <el-radio-group v-model="tab" size="small">
        <el-radio-button value="visual">可视化生成</el-radio-button>
        <el-radio-button value="manual">手写正则</el-radio-button>
      </el-radio-group>
    </div>

    <!-- 可视化生成 -->
    <template v-if="tab === 'visual'">
      <el-alert
        v-if="overwriteWarn"
        type="warning"
        :closable="false"
        show-icon
        title="当前已有一份正则，在下方重新选择/调整后会用生成结果覆盖它"
        style="margin-bottom: 8px"
      />
      <div v-if="avail.length > 1" class="rfp-row">
        <span class="rfp-label">匹配类型</span>
        <el-select v-model="templateId" placeholder="选择要识别的内容类型…" style="width: 100%">
          <el-option
            v-for="t in avail"
            :key="t.id"
            :label="t.label"
            :value="t.id"
          />
        </el-select>
      </div>

      <template v-if="def">
        <div v-for="o in def.options" :key="o.kind + o.key" class="rfp-row" :class="{ 'rfp-col': o.kind === 'text' }">
          <span class="rfp-label">{{ o.label }}</span>
          <el-switch
            v-if="o.kind === 'switch'"
            :model-value="boolOf(o.key)"
            :disabled="o.when ? !o.when(state) : false"
            size="small"
            @change="(v: string | number | boolean) => setBool(o.key, v)"
          />
          <el-input
            v-else-if="o.kind === 'text'"
            :model-value="strOf(o.key)"
            :placeholder="o.placeholder"
            @update:model-value="(v: string | number) => setText(o.key, v)"
          />
          <template v-else>
            <span class="rfp-sub">最少</span>
            <el-input-number v-model="state.minLen" :min="1" :max="64" size="small" controls-position="right" @change="regenerate" />
            <span class="rfp-sub">最多</span>
            <el-input-number v-model="state.maxLen" :min="1" :max="64" size="small" controls-position="right" @change="regenerate" />
          </template>
          <span v-if="o.hint" class="rfp-hint">{{ o.hint }}</span>
        </div>
      </template>
      <div v-if="!def" class="rfp-tip">选择上方“匹配类型”后，将自动生成正则、替换值与可复制的表达式。</div>

      <div class="rfp-gen">
        <div class="rfp-gen-head">
          <span class="rfp-label">生成的正则</span>
          <el-button link type="primary" size="small" :disabled="!modelValue" @click="copyPattern">复制</el-button>
        </div>
        <code class="rfp-code">{{ modelValue || '（待生成）' }}</code>
      </div>
    </template>

    <!-- 手写正则 -->
    <el-input
      v-else
      type="textarea"
      :rows="2"
      :model-value="modelValue"
      placeholder="合法正则表达式（RE2 语法，如 \b1[3-9]\d{9}\b）"
      @update:model-value="onManualInput"
    />
    <div v-if="modelValue && !patternValid" class="rfp-invalid">正则写法有误（服务端保存时同样会拒绝），请检查括号/反斜杠是否完整。</div>

    <!-- 样例自测 -->
    <div class="rfp-row rfp-col rfp-test">
      <span class="rfp-label">样例自测</span>
      <el-input
        v-model="sample"
        type="textarea"
        :rows="2"
        maxlength="400"
        show-word-limit
        placeholder="粘贴一段样例文本，实时查看命中数与脱敏效果（仅前端演示，以服务端引擎实际执行为准）"
      />
      <div v-if="testResult" class="rfp-result">
        <template v-if="testResult.count >= 0">
          <el-tag size="small" :type="testResult.count > 0 ? 'danger' : 'info'">
            {{ testResult.count > 0 ? `命中 ${testResult.count} 处` : '未命中' }}
          </el-tag>
          <div v-if="showReplacement && testResult.count > 0" class="rfp-preview">
            <span class="rfp-sub">脱敏预览</span>
            <pre class="rfp-preview-text">{{ testResult.preview }}</pre>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  defaultTemplateState,
  freshState,
  previewMask,
  templateById,
  templatesFor,
} from '../utils/regexBuilder'
import type { OptionKey, RuleKind, TemplateId, TemplateState } from '../utils/regexBuilder'

const props = withDefaults(defineProps<{
  ruleKind: RuleKind
  modelValue: string
  /** 是否按脱敏规则处理：true 时自动预填替换值、展示脱敏预览 */
  showReplacement?: boolean
  /** 脱敏预览使用的替换串（来自页面表单） */
  previewReplacement?: string
}>(), {
  showReplacement: false,
  previewReplacement: '',
})

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void
  (e: 'set-replacement', v: string): void
}>()

const tab = ref<'visual' | 'manual'>(props.modelValue.trim() ? 'manual' : 'visual')
const templateId = ref<TemplateId | ''>('')
const state = reactive<TemplateState>(defaultTemplateState())
const builderOwned = ref(false)
const sample = ref('')

const avail = computed(() => templatesFor(props.ruleKind))
const def = computed(() => (templateId.value ? templateById(templateId.value) : undefined))

const patternValid = computed(() => {
  if (!props.modelValue.trim()) return false
  try {
    new RegExp(props.modelValue)
    return true
  } catch {
    return false
  }
})

// 已有非本次生成的正则时，进入可视化会提示覆盖
const overwriteWarn = computed(
  () => tab.value === 'visual' && !!props.modelValue.trim() && !builderOwned.value,
)

function applyTemplate(id: TemplateId | '') {
  templateId.value = id
  const t = id ? templateById(id) : undefined
  if (t) Object.assign(state, freshState(t))
  regenerate()
}

function regenerate() {
  if (tab.value !== 'visual') return
  const t = def.value
  if (!t) return
  const p = t.build(state)
  if (p === null) return
  if (props.modelValue !== p) emit('update:modelValue', p)
  builderOwned.value = true
  if (props.showReplacement && t.defaultReplacement !== props.previewReplacement) {
    emit('set-replacement', t.defaultReplacement)
  }
}

function boolOf(k: OptionKey): boolean {
  return Boolean(state[k])
}

function strOf(k: OptionKey): string {
  return String(state[k] ?? '')
}

function setOpt(k: OptionKey, v: unknown): void {
  ;(state as unknown as Record<OptionKey, unknown>)[k] = v
  regenerate()
}

function setBool(k: OptionKey, v: string | number | boolean): void {
  setOpt(k, Boolean(v))
}

function setText(k: OptionKey, v: string | number): void {
  setOpt(k, typeof v === 'string' ? v : String(v))
}

function onManualInput(v: string): void {
  builderOwned.value = false
  emit('update:modelValue', v)
}

function onTemplateChange(v: TemplateId | '') {
  applyTemplate(v)
}

async function copyPattern(): Promise<void> {
  try {
    await navigator.clipboard.writeText(props.modelValue)
    ElMessage.success('正则已复制')
  } catch {
    ElMessage.error('复制失败，请手动选择复制')
  }
}

const testResult = computed(() => {
  if (!sample.value.trim() || !props.modelValue.trim() || !patternValid.value) return null
  const r = previewMask(sample.value, props.modelValue, props.previewReplacement)
  return r
})

watch(
  () => props.ruleKind,
  () => {
    // 规则类型切换后，旧类型生成的正则语义已不适用，清空（仅当它确实由本构建器产生）
    if (tab.value === 'visual' && builderOwned.value && props.modelValue) {
      emit('update:modelValue', '')
      builderOwned.value = false
    }
    const stillApplies = templateId.value ? avail.value.some((t) => t.id === templateId.value) : false
    if (!stillApplies) {
      if (avail.value.length === 1) {
        applyTemplate(avail.value[0].id)
      } else {
        templateId.value = ''
        regenerate()
      }
    }
  },
)

onMounted(() => {
  if (avail.value.length === 1 && !templateId.value) {
    applyTemplate(avail.value[0].id)
  }
})
</script>

<style scoped>
.rfp { width: 100%; }
.rfp-row { display: flex; align-items: center; gap: 8px; margin: 6px 0; flex-wrap: wrap; }
.rfp-col { flex-direction: column; align-items: stretch; }
.rfp-label { flex: none; font-size: 13px; color: var(--el-text-color-regular); width: 88px; text-align: right; }
.rfp-sub { font-size: 12px; color: var(--el-text-color-secondary); }
.rfp-hint { font-size: 12px; color: var(--el-text-color-secondary); }
.rfp-tip { font-size: 12px; color: var(--el-text-color-secondary); padding: 2px 0 6px; }
.rfp-gen { margin: 6px 0; }
.rfp-gen-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.rfp-code {
  display: block;
  padding: 6px 10px;
  background: var(--el-fill-color-light);
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  font-family: var(--el-font-family-mono, monospace);
  font-size: 12px;
  word-break: break-all;
  white-space: pre-wrap;
  line-height: 1.6;
}
.rfp-invalid { margin-top: 4px; font-size: 12px; color: var(--el-color-danger); }
.rfp-test { margin-top: 8px; border-top: 1px dashed var(--el-border-color); padding-top: 8px; }
.rfp-result { margin-top: 6px; }
.rfp-preview { margin-top: 6px; }
.rfp-preview-text {
  margin: 4px 0 0;
  padding: 6px 10px;
  background: var(--el-bg-color-page);
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 120px;
  overflow: auto;
}
</style>
