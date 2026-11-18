<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue: string | number
    label?: string
    type?: string
    min?: number
    max?: number
    step?: number
    hint?: string
    placeholder?: string
    disabled?: boolean
    inputId?: string
    autocomplete?: string
  }>(),
  { type: 'text', disabled: false, autocomplete: 'off' }
)

const emit = defineEmits<{ 'update:modelValue': [value: string | number] }>()

let fallbackSeq = 0
const fallbackId = `field-${++fallbackSeq}-${Math.random().toString(36).slice(2, 7)}`
const id = computed(() => props.inputId || fallbackId)

function onInput(event: Event): void {
  const el = event.target as HTMLInputElement
  if (props.type === 'number') {
    const n = el.value === '' ? 0 : Number(el.value)
    emit('update:modelValue', Number.isFinite(n) ? n : 0)
    return
  }
  emit('update:modelValue', el.value)
}
</script>

<template>
  <div class="field">
    <label v-if="label" class="label" :for="id">{{ label }}</label>
    <input
      :id="id"
      class="input"
      :type="type"
      :value="modelValue"
      :min="min"
      :max="max"
      :step="step"
      :placeholder="placeholder"
      :disabled="disabled"
      :autocomplete="autocomplete"
      :inputmode="type === 'number' ? 'numeric' : undefined"
      @input="onInput"
    />
    <span v-if="hint" class="hint">{{ hint }}</span>
  </div>
</template>
