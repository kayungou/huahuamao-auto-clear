<script setup lang="ts">
import { iconPaths, type IconName } from '@/lib/icons'

withDefaults(defineProps<{ name: IconName; size?: number; strokeWidth?: number }>(), {
  size: 18,
  strokeWidth: 1.6
})
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path v-for="(d, index) in iconPaths[name]" :key="index" :d="d" />
  </svg>
</template>
