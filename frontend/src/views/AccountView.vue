<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { fmtInt, initial, numericEntries, truncateMiddle } from '@/lib/format'
import { currentAccount, refreshMe, session } from '@/stores/session'
import { toast } from '@/stores/ui'
import PageHeader from '@/components/PageHeader.vue'
import UiBadge from '@/components/UiBadge.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
import UiStat from '@/components/UiStat.vue'

const ITEM_LABELS: Record<string, string> = {
  hint: '提示',
  can: '撤回',
  map: '地图',
  pass: '跳关',
  spent: '已花',
  at: '道具时间',
  sk: 'sk',
  skin: '皮肤',
  seen: '已看',
  ul: 'ul'
}
const ITEM_ORDER = ['hint', 'can', 'map', 'pass', 'spent', 'at', 'sk', 'skin', 'seen', 'ul']

const user = computed(() => session.me?.user ?? null)
const modes = computed(() => session.me?.modes ?? null)

const fishTotal = computed(() => numericEntries(modes.value?.fish).reduce((sum, [, v]) => sum + v, 0))

const items = computed(() => {
  const map = session.me?.items ?? {}
  return Object.keys(map).sort((a, b) => {
    const ia = ITEM_ORDER.indexOf(a)
    const ib = ITEM_ORDER.indexOf(b)
    if (ia !== ib) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
    return a.localeCompare(b)
  })
})

const reveal = ref(false)
const copied = ref(false)

const tokenText = computed(() => {
  const t = session.token
  if (!t) return ''
  if (reveal.value) return t
  return `${t.slice(0, 6)}••••••••${t.slice(-4)}`
})

async function copyToken(): Promise<void> {
  if (!session.token) return
  try {
    await navigator.clipboard.writeText(session.token)
    copied.value = true
    toast('Token 已复制到剪贴板')
  } catch {
    toast('复制失败，请手动选择文本复制', 'error')
  }
}
</script>

<template>
  <PageHeader title="游戏账号" desc="统计、道具与本机登录态">
    <template #actions>
      <UiButton
        v-if="session.token"
        variant="secondary"
        icon="refresh"
        :disabled="session.loadingMe"
        @click="refreshMe"
      >
        刷新数据
      </UiButton>
    </template>
  </PageHeader>

  <div v-if="!session.token" class="stack">
    <UiCard>
      <UiEmpty icon="qr" title="还没有登录" desc="扫码后 Token 会保存在本机 Cookie，服务端不保存任何账号。">
        <RouterLink to="/scan" class="btn btn-primary btn-sm">去扫码登录</RouterLink>
      </UiEmpty>
    </UiCard>
  </div>

  <div v-else class="stack">
    <UiCard>
      <div class="account-line">
        <span class="avatar avatar-lg">{{ initial(currentAccount?.nickname || '') }}</span>
        <div class="list-main">
          <div class="row" style="gap: 8px; flex-wrap: wrap">
            <span class="list-title">{{ currentAccount?.nickname || '已登录' }}</span>
            <UiBadge v-if="user" tone="ok" label="数据已加载" dot />
            <UiBadge v-else tone="idle" label="Token 已保存" dot />
          </div>
          <span class="list-meta mono">{{ truncateMiddle(currentAccount?.openid || '', 16, 6) }}</span>
        </div>
        <div class="row" style="gap: 8px; flex-wrap: wrap">
          <RouterLink to="/scan" class="btn btn-ghost btn-sm">重新扫码</RouterLink>
        </div>
      </div>

      <div class="token-box">
        <div class="row" style="justify-content: space-between; gap: 12px; flex-wrap: wrap">
          <span class="label">本机 Cookie 中的 Token（服务端不保存）</span>
          <div class="row" style="gap: 6px">
            <UiButton size="sm" variant="ghost" @click="reveal = !reveal">{{ reveal ? '隐藏' : '显示' }}</UiButton>
            <UiButton size="sm" variant="secondary" @click="copyToken">{{ copied ? '已复制' : '复制' }}</UiButton>
          </div>
        </div>
        <code class="token-text">{{ tokenText }}</code>
      </div>
    </UiCard>

    <UiCard v-if="user" title="账号统计">
      <div class="stat-grid">
        <UiStat label="星星" :value="fmtInt(user.stars)" />
        <UiStat label="已通关" :value="fmtInt(user.cleared)" />
        <UiStat label="三星" :value="fmtInt(user.three)" />
        <UiStat label="最高关" :value="fmtInt(user.top)" />
        <UiStat label="无尽" :value="fmtInt(user.endless)" unit="层" />
        <UiStat label="小鱼" :value="fmtInt(fishTotal)" />
        <UiStat label="数独最佳" :value="fmtInt(modes?.sudoku_best ?? 0)" />
        <UiStat label="消消喵最佳" :value="fmtInt(modes?.match_best ?? 0)" />
      </div>
      <p class="hint" style="margin-top: 12px">
        昵称 {{ user.nickname || '—' }} · ID {{ user.id }} · 皮肤 {{ user.skin || '默认' }}
      </p>
    </UiCard>

    <UiCard v-if="items.length" title="道具 / 钱包">
      <div class="chips">
        <span v-for="key in items" :key="key" class="chip">
          {{ ITEM_LABELS[key] || key }}
          <b>{{ fmtInt(session.me?.items?.[key] ?? 0) }}</b>
        </span>
      </div>
    </UiCard>

    <p v-if="!user" class="hint">数据还没加载：检查网络后点「刷新数据」；若提示登录态失效，重新扫码即可。</p>
  </div>
</template>

<style scoped>
.account-line {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.token-box {
  margin-top: 16px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface-2);
}

.token-text {
  display: block;
  margin-top: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  word-break: break-all;
  color: var(--text-2);
}
</style>
