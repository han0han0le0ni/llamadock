<script setup>
import { computed } from 'vue'

const props = defineProps({
  param: { type: Object, required: true },
  modelValue: { default: null },
  disabled: { type: Boolean, default: false },
  disabledReason: { type: String, default: '' },
  recommended: { default: null },
  source: { type: String, default: '全局' },
})
const emit = defineEmits(['update:modelValue'])

const p = computed(() => props.param)
const isList = computed(() => p.value.kind === 'strlist')
const listText = computed({
  get: () => (Array.isArray(props.modelValue) ? props.modelValue.join('\n') : ''),
  set: (v) =>
    emit(
      'update:modelValue',
      v
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
    ),
})

const numValue = computed({
  get: () => Number(props.modelValue ?? 0),
  set: (v) => emit('update:modelValue', Number(v)),
})

const strValue = computed({
  get: () => (props.modelValue == null ? '' : String(props.modelValue)),
  set: (v) => emit('update:modelValue', v),
})

const boolValue = computed(() => props.modelValue === true || props.modelValue === 'true')

const isRecommended = computed(
  () => props.recommended != null && String(props.recommended) === String(props.modelValue),
)
</script>

<template>
  <div class="param-row" :class="{ disabled }">
    <div class="param-name">
      <span class="k">
        {{ p.label }}
        <span v-if="p.common" class="badge" style="margin-left: 6px">常用</span>
        <span v-if="!p.runtime" class="badge lock" style="margin-left: 6px">需重启</span>
        <span v-if="!p.overridable" class="badge lock" style="margin-left: 6px">🔒 请求不可覆盖</span>
      </span>
      <span class="d">{{ p.desc }}</span>
      <span v-if="disabled && disabledReason" class="d" style="color: var(--warn-text)">⚠ {{ disabledReason }}</span>
    </div>

    <div class="param-control">
      <!-- 布尔 -->
      <template v-if="p.kind === 'bool'">
        <div
          class="switch"
          :class="{ on: boolValue }"
          :style="disabled ? 'pointer-events:none' : ''"
          @click="emit('update:modelValue', !boolValue)"
        >
          <i></i>
        </div>
        <span class="hint">{{ boolValue ? '开' : '关' }}</span>
      </template>

      <!-- 枚举 -->
      <template v-else-if="p.kind === 'enum'">
        <select v-model="strValue" :disabled="disabled" style="max-width: 170px">
          <option v-for="o in p.options" :key="o" :value="o">{{ o }}</option>
        </select>
      </template>

      <!-- 字符串数组 -->
      <template v-else-if="isList">
        <textarea v-model="listText" rows="2" :disabled="disabled" placeholder="每行一个" />
      </template>

      <!-- 数值 -->
      <template v-else-if="p.kind === 'int' || p.kind === 'float'">
        <input
          type="range"
          style="flex: 1"
          :min="p.min ?? 0"
          :max="p.max ?? 100"
          :step="p.step ?? 1"
          v-model.number="numValue"
          :disabled="disabled"
        />
        <input
          type="number"
          style="width: 110px"
          :min="p.min"
          :max="p.max"
          :step="p.step ?? 1"
          v-model.number="numValue"
          :disabled="disabled"
        />
        <span class="hint" v-if="p.unit">{{ p.unit }}</span>
      </template>

      <!-- 普通字符串 -->
      <template v-else>
        <input type="text" v-model="strValue" :disabled="disabled" />
      </template>

      <span v-if="isRecommended" class="badge info" title="与硬件推荐值一致">推荐值</span>
      <button
        v-if="recommended != null && !isRecommended"
        class="sm ghost"
        :disabled="disabled"
        title="填回硬件推荐值"
        @click="emit('update:modelValue', recommended)"
      >
        用推荐 {{ recommended }}
      </button>
      <span class="hint" style="min-width: 34px; text-align: right">{{ source }}</span>
    </div>
  </div>
</template>

<style scoped>
textarea {
  max-width: 320px;
  resize: vertical;
  font-family: var(--mono);
  font-size: 12px;
}
</style>
