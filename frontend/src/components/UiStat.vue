<script setup lang="ts">
withDefaults(defineProps<{ label: string; value: string | number; unit?: string }>(), { unit: '' })
</script>

<template>
  <div class="stat">
    <span class="stat-label">{{ label }}</span>
    <span class="stat-value">{{ value }}<small v-if="unit">{{ unit }}</small></span>
  </div>
</template>
