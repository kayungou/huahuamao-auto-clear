<script setup lang="ts">
import { RouterLink, useRouter } from 'vue-router'
import type { LoginSession } from '@/api/types'
import { fmtInt, initial, truncateMiddle } from '@/lib/format'
import { logout, refreshMe, saveLogin, session } from '@/stores/session'
import { toast } from '@/stores/ui'
import PageHeader from '@/components/PageHeader.vue'
import QrPanel from '@/components/QrPanel.vue'
import UiBadge from '@/components/UiBadge.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'

const router = useRouter()

async function onConfirmed(login: LoginSession): Promise<void> {
  await saveLogin(login)
  toast(`已登录：${login.account.nickname || login.account.openid}`)
  await router.push('/account')
}

async function onRefresh(): Promise<void> {
  await refreshMe()
}

function onLogout(): void {
  logout()
  void router.push('/scan')
}
</script>

<template>
  <PageHeader title="扫码登录" desc="扫码后 Token 写入本机 Cookie，服务端不保存账号" />

  <div class="stack">
    <UiCard title="微信扫码" subtitle="二维码由本服务向微信换取；Token 只下发到你的浏览器">
      <QrPanel @confirmed="onConfirmed" />
    </UiCard>

    <UiCard v-if="session.account" title="当前登录态">
      <div class="row" style="gap: 12px; flex-wrap: wrap">
        <span class="avatar avatar-lg">{{ initial(session.account.nickname) }}</span>
        <div class="list-main">
          <div class="row" style="gap: 8px; flex-wrap: wrap">
            <span class="list-title">{{ session.account.nickname || '未命名账号' }}</span>
            <UiBadge v-if="session.me" tone="ok" label="已登录游戏" dot />
            <UiBadge v-else tone="idle" label="登录态已保存" dot />
          </div>
          <span class="list-meta mono">{{ truncateMiddle(session.account.openid, 14, 6) }}</span>
          <span v-if="session.me?.user" class="hint">星星 {{ fmtInt(session.me.user.stars) }} · 已通关 {{ fmtInt(session.me.user.cleared) }}</span>
        </div>
      </div>
      <div class="row" style="gap: 8px; margin-top: 16px; flex-wrap: wrap">
        <UiButton variant="secondary" icon="refresh" :disabled="session.loadingMe" @click="onRefresh">刷新数据</UiButton>
        <RouterLink to="/ops" class="btn btn-primary">去操作台</RouterLink>
        <UiButton variant="ghost" icon="logout" @click="onLogout">在本机退出登录</UiButton>
      </div>
    </UiCard>

    <p class="hint">
      提示：Token 存在浏览器的 <span class="mono">sgz_session</span> Cookie 中，有效期内刷新页面或重启服务都不需要重新扫码；换浏览器、清理 Cookie 或退出登录后需重新扫码。
    </p>
  </div>
</template>
