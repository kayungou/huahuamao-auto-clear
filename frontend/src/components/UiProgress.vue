<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ value: number; max?: number; label?: string; hint?: string }>(), {
  max: 0,
  label: '',
  hint: ''
})

const percent = computed(() => {
  if (!props.max || props.max <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((props.value / props.max) * 100)))
})
</script>

<template>
  <div>
    <div v-if="label || hint" class="progress-meta">
      <span v-if="label">{{ label }}</span>
      <span v-if="hint">{{ hint }}</span>
    </div>
    <div class="progress" role="progressbar" :aria-valuemin="0" :aria-valuemax="max || 100" :aria-valuenow="value">
      <div class="progress-fill" :style="{ width: `${percent}%` }" />
    </div>
  </div>
</template>
