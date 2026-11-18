<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { initial, truncateMiddle } from '@/lib/format'
import { currentAccount, logout, refreshMe, session } from '@/stores/session'
import { prefs, setTheme, type Theme } from '@/stores/prefs'
import { confirmDialog, toast } from '@/stores/ui'
import { clearHistory, jobs } from '@/stores/jobs'
import PageHeader from '@/components/PageHeader.vue'
import UiBadge from '@/components/UiBadge.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
import UiInput from '@/components/UiInput.vue'

const themes: Array<{ value: Theme; label: string }> = [
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' }
]

const version = computed(() => session.appInfo?.version || '1.0.0')
const appID = computed(() => session.appInfo?.app_id || 'wx0a7720b2b180dd29')

async function onLogout(): Promise<void> {
  const ok = await confirmDialog({
    title: '退出本机登录？',
    message: '将清除此浏览器 Cookie 中的 Token 与账号资料；服务端本来就不保存这些数据，之后需重新扫码。',
    confirmText: '退出登录',
    danger: true
  })
  if (ok) logout()
}

async function onClearHistory(): Promise<void> {
  const ok = await confirmDialog({
    title: '清空任务历史？',
    message: '只清空本机浏览器保存的任务记录，不影响服务端数据。',
    confirmText: '清空',
    danger: true
  })
  if (ok) {
    clearHistory()
    toast('已清空任务历史')
  }
}

async function onResetPrefs(): Promise<void> {
  const ok = await confirmDialog({
    title: '恢复默认参数？',
    message: '起止关、消消喵目标、无尽层数、广告条数将恢复默认值，主题保持不变。',
    confirmText: '恢复'
  })
  if (!ok) return
  prefs.clearFrom = 1
  prefs.clearTo = 220
  prefs.matchBest = 200
  prefs.endlessFloor = 200
  prefs.adCount = 12
  prefs.rankType = 'stars'
  toast('已恢复默认参数')
}
</script>

<template>
  <PageHeader title="设置" desc="外观、默认参数与本机登录态" />

  <div class="stack">
    <UiCard title="外观">
      <div class="row" style="justify-content: space-between; flex-wrap: wrap; gap: 12px">
        <div>
          <div class="list-title">主题</div>
          <p class="hint">默认浅色；深色适合长时间查看日志。选择会保存在本机。</p>
        </div>
        <div class="segmented" role="group" aria-label="主题">
          <button
            v-for="item in themes"
            :key="item.value"
            type="button"
            :class="{ 'is-active': prefs.theme === item.value }"
            @click="setTheme(item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>
    </UiCard>

    <UiCard title="默认参数" subtitle="操作台会使用这里的默认值">
      <div class="op-grid">
        <UiInput v-model="prefs.clearFrom" label="一键通关 · 起始关" type="number" :min="1" :max="9999" />
        <UiInput v-model="prefs.clearTo" label="一键通关 · 结束关" type="number" :min="1" :max="9999" />
        <UiInput v-model="prefs.matchBest" label="消消喵 · 目标关数" type="number" :min="1" :max="9999" />
        <UiInput v-model="prefs.endlessFloor" label="无尽模式 · 目标层数" type="number" :min="1" :max="9999" />
        <UiInput v-model="prefs.adCount" label="广告记录 · 条数" type="number" :min="1" :max="200" />
      </div>
      <div class="row" style="margin-top: 14px">
        <UiButton variant="ghost" size="sm" @click="onResetPrefs">恢复默认参数</UiButton>
      </div>
    </UiCard>

    <UiCard title="本机登录态" subtitle="Token 只保存在此浏览器的 Cookie">
      <div v-if="currentAccount" class="row" style="gap: 12px; flex-wrap: wrap">
        <span class="avatar avatar-lg">{{ initial(currentAccount.nickname) }}</span>
        <div class="list-main">
          <div class="row" style="gap: 8px; flex-wrap: wrap">
            <span class="list-title">{{ currentAccount.nickname || '未命名账号' }}</span>
            <UiBadge v-if="session.me" tone="ok" label="已登录游戏" dot />
            <UiBadge v-else tone="idle" label="登录态已保存" dot />
          </div>
          <span class="list-meta mono">{{ truncateMiddle(currentAccount.openid, 16, 6) }}</span>
        </div>
        <div class="row" style="gap: 8px; flex-wrap: wrap">
          <UiButton size="sm" variant="secondary" icon="refresh" :disabled="session.loadingMe" @click="refreshMe">刷新数据</UiButton>
          <UiButton size="sm" variant="danger" icon="logout" @click="onLogout">退出登录</UiButton>
        </div>
      </div>
      <UiEmpty v-else icon="user" title="未登录" desc="登录态只存在你的浏览器里，服务端不保存账号、Token 或微信会话。">
        <RouterLink to="/scan" class="btn btn-primary btn-sm">去扫码登录</RouterLink>
      </UiEmpty>
      <p class="hint" style="margin-top: 12px">
        清除浏览器 Cookie 或换一台设备后需要重新扫码；服务端没有任何账号数据可被他人读取。
      </p>
    </UiCard>

    <UiCard title="本地数据">
      <div class="row" style="justify-content: space-between; flex-wrap: wrap; gap: 12px">
        <div>
          <div class="list-title">任务历史</div>
          <p class="hint">当前保存 {{ jobs.history.length }} 条，仅在本机浏览器。</p>
        </div>
        <UiButton variant="danger" size="sm" icon="trash" @click="onClearHistory">清空任务历史</UiButton>
      </div>
    </UiCard>

    <UiCard title="关于">
      <div class="kv"><span class="kv-label">版本</span><span class="kv-value">v{{ version }}</span></div>
      <div class="kv"><span class="kv-label">小程序 AppID</span><span class="kv-value mono">{{ appID }}</span></div>
      <div class="kv">
        <span class="kv-label">运行方式</span>
        <span class="kv-value">Go 服务 + 内嵌前端，全部资源离线可用</span>
      </div>
      <div class="kv">
        <span class="kv-label">数据存放</span>
        <span class="kv-value">账号与 Token 仅在本机浏览器 Cookie（sgz_session），服务端不落盘</span>
      </div>
      <p class="hint" style="margin-top: 14px">
        本工具仅用于已授权的测试账号。服务端进度只增不减，请确认目标后再执行写操作。
      </p>
    </UiCard>
  </div>
</template>
