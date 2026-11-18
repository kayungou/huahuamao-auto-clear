<script setup lang="ts">
import type { IconName } from '@/lib/icons'
import UiIcon from './UiIcon.vue'

withDefaults(defineProps<{ icon?: IconName; title: string; desc?: string }>(), { icon: 'info', desc: '' })
</script>

<template>
  <div class="empty">
    <span class="empty-icon"><UiIcon :name="icon" :size="20" /></span>
    <span class="empty-title">{{ title }}</span>
    <span v-if="desc" class="empty-desc">{{ desc }}</span>
    <slot />
  </div>
</template>
