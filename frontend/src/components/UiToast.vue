<script setup lang="ts">
import { ui } from '@/stores/ui'
import UiIcon from './UiIcon.vue'
</script>

<template>
  <div class="toasts" aria-live="polite" aria-atomic="false">
    <div v-for="item in ui.toasts" :key="item.id" class="toast" :class="`is-${item.kind}`">
      <UiIcon :name="item.kind === 'error' ? 'alert' : 'check'" :size="15" />
      <span>{{ item.text }}</span>
    </div>
  </div>
</template>
