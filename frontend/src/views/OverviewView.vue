<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { IconName } from '@/lib/icons'
import { fmtInt, fmtTime, initial, truncateMiddle } from '@/lib/format'
import { currentAccount, refreshMe, session } from '@/stores/session'
import { jobs } from '@/stores/jobs'
import PageHeader from '@/components/PageHeader.vue'
import UiBadge from '@/components/UiBadge.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
import UiIcon from '@/components/UiIcon.vue'

const user = computed(() => session.me?.user ?? null)
const modes = computed(() => session.me?.modes ?? null)

const recent = computed(() => jobs.history.slice(0, 5))

const quick: Array<{ to: string; icon: IconName; title: string; desc: string }> = [
  { to: '/scan', icon: 'qr', title: '扫码登录', desc: '登录或更换游戏账号' },
  { to: '/account', icon: 'user', title: '游戏账号', desc: '统计、道具与本机 Token' },
  { to: '/ops', icon: 'sliders', title: '操作台', desc: '通关、小游戏、道具与查询' },
  { to: '/jobs', icon: 'list', title: '任务与日志', desc: '查看进度、日志与结果' }
]

function statusTone(status: string): 'ok' | 'error' | 'neutral' {
  if (status === 'done') return 'ok'
  if (status === 'error') return 'error'
  return 'neutral'
}

function statusLabel(status: string): string {
  if (status === 'done') return '完成'
  if (status === 'error') return '失败'
  return '运行中'
}
</script>

<template>
  <PageHeader title="概览" desc="账号状态与常用入口">
    <template #actions>
      <UiButton v-if="session.token" variant="secondary" icon="refresh" :disabled="session.loadingMe" @click="refreshMe">
        刷新数据
      </UiButton>
    </template>
  </PageHeader>

  <div class="stack">
    <UiCard v-if="!session.token" title="开始使用" subtitle="两步接入你的测试账号">
      <div class="steps">
        <div class="step-row"><span class="step-num">1</span><span>在「扫码登录」页用手机微信扫码，Token 会写入本机 Cookie，服务端不保存。</span></div>
        <div class="step-row"><span class="step-num">2</span><span>回到「操作台」选择目标关卡并执行，进度可在「任务与日志」查看。</span></div>
      </div>
      <div class="row" style="margin-top: 16px">
        <RouterLink to="/scan" class="btn btn-primary">去扫码登录</RouterLink>
      </div>
    </UiCard>

    <template v-else>
      <UiCard>
        <div class="account-head">
          <span class="avatar avatar-lg">{{ initial(currentAccount?.nickname || '') }}</span>
          <div class="account-main">
            <div class="row" style="gap: 8px; flex-wrap: wrap">
              <span class="account-name">{{ currentAccount?.nickname || '已登录' }}</span>
              <UiBadge v-if="user" tone="ok" label="已登录游戏" dot />
              <UiBadge v-else tone="idle" label="登录态已保存" dot />
            </div>
            <span class="account-meta mono">
              {{ truncateMiddle(currentAccount?.openid || '', 12, 6) }}
            </span>
          </div>
          <div class="account-actions">
            <RouterLink to="/account" class="btn btn-secondary btn-sm">账号详情</RouterLink>
            <RouterLink to="/ops" class="btn btn-primary btn-sm">去操作台</RouterLink>
          </div>
        </div>

        <div v-if="user" class="metrics" style="margin-top: 20px">
          <div class="metric">
            <span class="metric-label">星星</span>
            <span class="metric-value">{{ fmtInt(user.stars) }}</span>
          </div>
          <div class="metric">
            <span class="metric-label">已通关</span>
            <span class="metric-value">{{ fmtInt(user.cleared) }}</span>
          </div>
          <div class="metric">
            <span class="metric-label">三星</span>
            <span class="metric-value">{{ fmtInt(user.three) }}</span>
          </div>
          <div class="metric">
            <span class="metric-label">最高关</span>
            <span class="metric-value">{{ fmtInt(user.top) }}</span>
          </div>
          <div v-if="modes" class="metric">
            <span class="metric-label">消消喵</span>
            <span class="metric-value">{{ fmtInt(modes.match_best) }}</span>
          </div>
        </div>
        <p v-else class="hint" style="margin-top: 16px">
          登录态已保存在本机 Cookie，但账号数据还没读出来：点右上角「刷新数据」重试。
        </p>
      </UiCard>

      <div class="grid-2">
        <RouterLink v-for="item in quick" :key="item.to" :to="item.to" class="quick">
          <span class="quick-icon"><UiIcon :name="item.icon" :size="18" /></span>
          <span>
            <span class="quick-title">{{ item.title }}</span>
            <span class="quick-desc" style="display: block">{{ item.desc }}</span>
          </span>
        </RouterLink>
      </div>

      <UiCard title="最近任务" subtitle="仅保存在本机浏览器">
        <template #actions>
          <RouterLink to="/jobs" class="btn btn-ghost btn-sm">全部</RouterLink>
        </template>
        <div v-if="recent.length" class="list">
          <RouterLink v-for="item in recent" :key="item.id" to="/jobs" class="list-row" style="color: inherit">
            <UiBadge :tone="statusTone(item.status)" :label="statusLabel(item.status)" dot />
            <div class="list-main">
              <span class="list-title">{{ item.label }}</span>
              <span class="list-meta">{{ item.summary }}</span>
            </div>
            <span class="list-meta nowrap">{{ fmtTime(item.startedAt) }}</span>
          </RouterLink>
        </div>
        <UiEmpty v-else icon="list" title="还没有任务记录" desc="在操作台发起任务后，这里会显示最近几次执行结果。" />
      </UiCard>
    </template>
  </div>
</template>

<style scoped>
.account-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.account-main {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1;
}

.account-name {
  font-size: 16px;
  font-weight: 600;
}

.account-meta {
  overflow: hidden;
  font-size: 12px;
  color: var(--muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}
</style>
