<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import type { IconName } from '@/lib/icons'
import UiIcon from './UiIcon.vue'

interface TabItem {
  to: string
  icon: IconName
  label: string
}

const tabs: TabItem[] = [
  { to: '/', icon: 'overview', label: '概览' },
  { to: '/scan', icon: 'qr', label: '扫码' },
  { to: '/account', icon: 'user', label: '账号' },
  { to: '/ops', icon: 'sliders', label: '操作' },
  { to: '/jobs', icon: 'list', label: '任务' }
]

const route = useRoute()

function isActive(to: string): boolean {
  if (to === '/') return route.path === '/'
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <nav class="tabbar" aria-label="底部导航">
    <RouterLink
      v-for="tab in tabs"
      :key="tab.to"
      :to="tab.to"
      class="tab"
      :class="{ 'is-active': isActive(tab.to) }"
      :aria-current="isActive(tab.to) ? 'page' : undefined"
    >
      <UiIcon :name="tab.icon" :size="20" />
      <span>{{ tab.label }}</span>
    </RouterLink>
  </nav>
</template>
