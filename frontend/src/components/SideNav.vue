<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import type { IconName } from '@/lib/icons'
import { session, currentAccount } from '@/stores/session'
import { initial } from '@/lib/format'
import { jobRunning } from '@/stores/jobs'
import { prefs, toggleTheme } from '@/stores/prefs'
import UiBadge from './UiBadge.vue'
import UiButton from './UiButton.vue'
import UiIcon from './UiIcon.vue'

interface NavItem {
  to: string
  icon: IconName
  label: string
}

const items: NavItem[] = [
  { to: '/', icon: 'overview', label: '概览' },
  { to: '/scan', icon: 'qr', label: '扫码登录' },
  { to: '/account', icon: 'user', label: '游戏账号' },
  { to: '/ops', icon: 'sliders', label: '操作台' },
  { to: '/jobs', icon: 'list', label: '任务与日志' },
  { to: '/settings', icon: 'settings', label: '设置' }
]

const route = useRoute()

function isActive(to: string): boolean {
  if (to === '/') return route.path === '/'
  return route.path === to || route.path.startsWith(`${to}/`)
}

const healthTone = computed(() => {
  if (session.health === 'ok') return 'ok' as const
  if (session.health === 'fail') return 'error' as const
  return 'idle' as const
})

const healthText = computed(() => {
  if (session.health === 'ok') return '服务正常'
  if (session.health === 'fail') return '服务未连接'
  return '检测中'
})

const accountText = computed(() => currentAccount.value?.nickname || '未登录')
</script>

<template>
  <aside class="side">
    <RouterLink to="/" class="side-brand" style="color: inherit; text-decoration: none">
      <span class="brand-mark">闯</span>
      <span class="brand-text">
        <span class="brand-name">闯关助手</span>
        <span class="brand-meta">Web 控制台</span>
      </span>
    </RouterLink>

    <nav class="nav" aria-label="主导航">
      <RouterLink
        v-for="item in items"
        :key="item.to"
        :to="item.to"
        class="nav-item"
        :class="{ 'is-active': isActive(item.to) }"
        :aria-current="isActive(item.to) ? 'page' : undefined"
      >
        <UiIcon :name="item.icon" :size="17" />
        <span>{{ item.label }}</span>
        <span v-if="item.to === '/jobs' && jobRunning" class="nav-dot" aria-label="任务执行中" />
      </RouterLink>
    </nav>

    <div class="side-foot">
      <RouterLink to="/account" class="side-account">
        <span class="avatar" style="width: 26px; height: 26px; font-size: 11px">
          <template v-if="currentAccount">{{ initial(currentAccount.nickname) }}</template>
          <UiIcon v-else name="user" :size="13" />
        </span>
        <span class="truncate">{{ accountText }}</span>
      </RouterLink>
      <div class="side-status">
        <UiBadge :tone="healthTone" :label="healthText" dot />
        <span v-if="session.appInfo">v{{ session.appInfo.version }}</span>
      </div>
      <UiButton variant="ghost" size="sm" :icon="prefs.theme === 'dark' ? 'sun' : 'moon'" block @click="toggleTheme">
        {{ prefs.theme === 'dark' ? '浅色模式' : '深色模式' }}
      </UiButton>
    </div>
  </aside>
</template>

<style scoped>
.nav-dot {
  width: 6px;
  height: 6px;
  margin-left: auto;
  border-radius: 50%;
  background: var(--accent);
}

.side-account {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 6px;
  font-size: 12px;
  color: var(--text-2);
  text-decoration: none;
}

.side-account:hover {
  text-decoration: none;
  color: var(--text);
}
</style>
