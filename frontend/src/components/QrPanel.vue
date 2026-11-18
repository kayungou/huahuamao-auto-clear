<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive } from 'vue'
import { api } from '@/api/client'
import type { LoginSession, QRInfo, QRStatusName } from '@/api/types'
import { errText, toast } from '@/stores/ui'
import UiBadge from './UiBadge.vue'
import UiButton from './UiButton.vue'
import UiEmpty from './UiEmpty.vue'

const emit = defineEmits<{ confirmed: [session: LoginSession] }>()

const STATUS_TEXT: Record<QRStatusName, string> = {
  pending: '等待扫码',
  scanned: '已扫码，请在手机上确认',
  authorized: '已授权，正在保存账号',
  confirmed: '授权成功',
  expired: '二维码已过期',
  cancelled: '已取消扫码',
  error: '扫码失败，请重新生成',
  unknown: '状态未知，请重新生成'
}

const state = reactive({
  qr: null as QRInfo | null,
  status: 'pending' as QRStatusName,
  countdown: 0,
  loading: false,
  error: ''
})

let pollTimer: number | null = null
let tickTimer: number | null = null
let generation = 0

const active = computed(() => state.status === 'pending' || state.status === 'scanned' || state.status === 'authorized')
const tone = computed(() => {
  if (state.status === 'confirmed') return 'ok' as const
  if (state.status === 'expired' || state.status === 'cancelled' || state.status === 'unknown') return 'warn' as const
  return 'neutral' as const
})

function stop(): void {
  generation += 1
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer)
    pollTimer = null
  }
  if (tickTimer !== null) {
    window.clearInterval(tickTimer)
    tickTimer = null
  }
}

function schedule(): void {
  const g = generation
  pollTimer = window.setTimeout(() => void poll(g), 1500)
}

async function poll(g: number): Promise<void> {
  if (g !== generation || !state.qr) return
  try {
    const res = await api.pollQR(state.qr.id)
    if (g !== generation) return
    state.status = res.status
    state.error = ''
    if (res.status === 'confirmed' && res.session) {
      stop()
      emit('confirmed', res.session)
      return
    }
    if (!active.value) {
      stop()
      return
    }
    schedule()
  } catch (err) {
    if (g !== generation) return
    state.error = errText(err)
    schedule()
  }
}

function startTick(): void {
  if (tickTimer !== null) window.clearInterval(tickTimer)
  tickTimer = window.setInterval(() => {
    if (state.countdown > 0) state.countdown -= 1
    if (state.countdown <= 0) stop()
  }, 1000)
}

async function start(): Promise<void> {
  stop()
  state.loading = true
  state.error = ''
  state.status = 'pending'
  try {
    const qr = await api.newQR()
    state.qr = qr
    state.countdown = qr.expires_in
    startTick()
    schedule()
  } catch (err) {
    state.error = errText(err)
    state.qr = null
  } finally {
    state.loading = false
  }
}

function regenerate(): void {
  void start().then(() => toast('二维码已刷新'))
}

onMounted(() => void start())
onBeforeUnmount(stop)

defineExpose({ start })
</script>

<template>
  <div class="qr-wrap">
    <div class="qr-frame">
      <img v-if="state.qr" :src="state.qr.image" alt="微信登录二维码" class="qr-img" />
      <div v-else class="qr-img qr-placeholder">{{ state.loading ? '生成中…' : '二维码不可用' }}</div>
    </div>
    <div class="qr-side">
      <div class="row" style="gap: 8px; flex-wrap: wrap">
        <UiBadge :tone="tone" :label="STATUS_TEXT[state.status]" dot />
        <span v-if="active && state.countdown > 0" class="text-sm muted num">{{ state.countdown }}s 后过期</span>
      </div>
      <p class="hint">用手机微信扫码，并在手机上点击确认登录。</p>
      <p class="hint">扫码成功后 Token 会写入本机 Cookie，服务端不保存任何账号或凭证。</p>
      <p v-if="state.error" class="text-sm" style="color: var(--danger)">{{ state.error }}</p>
      <div class="row" style="gap: 8px">
        <UiButton variant="secondary" :disabled="state.loading" icon="refresh" @click="regenerate">
          {{ state.loading ? '生成中…' : '重新生成' }}
        </UiButton>
      </div>
      <UiEmpty
        v-if="state.status === 'expired'"
        icon="clock"
        title="二维码已过期"
        desc="点击「重新生成」获取新的二维码，过期后旧的二维码无法继续使用。"
      />
    </div>
  </div>
</template>

<style scoped>
.qr-wrap {
  display: flex;
  gap: 28px;
  align-items: flex-start;
  flex-wrap: wrap;
}

.qr-frame {
  padding: 10px;
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
  background: var(--surface);
}

.qr-img {
  display: block;
  width: 200px;
  height: 200px;
  border-radius: var(--radius-sm);
  background: #fff;
}

.qr-placeholder {
  display: grid;
  place-items: center;
  color: var(--faint);
  font-size: 13px;
  background: var(--surface-2);
}

.qr-side {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 220px;
  flex: 1;
}
</style>
