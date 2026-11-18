<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { jobRunning } from '@/stores/jobs'
import UiIcon from './UiIcon.vue'

const route = useRoute()
const title = computed(() => String(route.meta.title ?? '闯关助手'))
</script>

<template>
  <header class="topbar">
    <div class="row" style="gap: 10px; min-width: 0">
      <span class="brand-mark" style="width: 26px; height: 26px; font-size: 13px">闯</span>
      <span class="topbar-title truncate">{{ title }}</span>
    </div>
    <div class="topbar-actions">
      <RouterLink v-if="jobRunning" to="/jobs" class="btn btn-ghost btn-icon" aria-label="任务执行中">
        <span class="running-dot" />
      </RouterLink>
      <RouterLink to="/settings" class="btn btn-ghost btn-icon" aria-label="设置">
        <UiIcon name="settings" :size="18" />
      </RouterLink>
    </div>
  </header>
</template>

<style scoped>
.running-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  animation: pulse 1.6s var(--ease) infinite;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}
</style>
