<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'ok' | 'warn' | 'error' | 'idle' | 'neutral'; label: string; dot?: boolean }>(), {
  tone: 'neutral',
  dot: false
})
</script>

<template>
  <span class="badge" :class="[`badge-${tone}`, dot ? 'badge-with-dot' : '']">
    <span v-if="dot" class="badge-dot" />
    {{ label }}
  </span>
</template>
