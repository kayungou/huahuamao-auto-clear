<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { fmtDuration, fmtTime } from '@/lib/format'
import { ACTION_LABELS, clearHistory, jobElapsedMs, jobRunning, jobs, type HistoryItem } from '@/stores/jobs'
import { confirmDialog } from '@/stores/ui'
import LogViewer from '@/components/LogViewer.vue'
import PageHeader from '@/components/PageHeader.vue'
import UiBadge from '@/components/UiBadge.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
import UiProgress from '@/components/UiProgress.vue'

const job = computed(() => jobs.job)
const elapsed = computed(() => (job.value ? jobElapsedMs() : 0))
const resultText = computed(() => (job.value?.result ? JSON.stringify(job.value.result, null, 2) : ''))

const tone = computed(() => {
  if (!job.value) return 'neutral' as const
  if (job.value.status === 'error') return 'error' as const
  if (job.value.status === 'done') return 'ok' as const
  return 'neutral' as const
})

const statusText = computed(() => {
  if (!job.value) return '空闲'
  if (job.value.status === 'error') return '失败'
  if (job.value.status === 'done') return '完成'
  return '执行中'
})

function clearLogs(): void {
  if (jobs.job) jobs.job.logs = []
}

async function onClearHistory(): Promise<void> {
  const ok = await confirmDialog({
    title: '清空任务历史？',
    message: '只清空本机浏览器保存的任务记录，不影响服务端账号数据。',
    confirmText: '清空',
    danger: true
  })
  if (ok) clearHistory()
}

function historyTone(item: HistoryItem): 'ok' | 'error' | 'neutral' {
  if (item.status === 'done') return 'ok'
  if (item.status === 'error') return 'error'
  return 'neutral'
}

function historyLabel(item: HistoryItem): string {
  if (item.status === 'done') return '完成'
  if (item.status === 'error') return '失败'
  return '运行中'
}

function historyDuration(item: HistoryItem): string {
  if (!item.finishedAt) return '—'
  return fmtDuration(item.finishedAt - item.startedAt)
}
</script>

<template>
  <PageHeader title="任务与日志" desc="当前任务进度、日志与本地历史">
    <template #actions>
      <UiButton
        v-if="jobs.history.length"
        variant="ghost"
        size="sm"
        icon="trash"
        @click="onClearHistory"
      >
        清空历史
      </UiButton>
    </template>
  </PageHeader>

  <div class="stack">
    <UiCard v-if="job" :title="`当前任务 · ${jobs.jobId}`">
      <template #actions>
        <UiBadge :tone="tone" :label="statusText" dot />
      </template>

      <div class="job-head">
        <span class="job-phase">{{ job.phase || '执行中' }}</span>
        <span class="job-meta">
          <template v-if="job.total > 0">{{ job.cur }} / {{ job.total }} · </template>
          用时 {{ fmtDuration(elapsed) }}
        </span>
      </div>
      <UiProgress v-if="job.total > 0" :value="job.cur" :max="job.total" />

      <p v-if="job.error" class="text-sm" style="margin-top: 12px; color: var(--danger)">
        {{ job.error }}
      </p>

      <div class="divider" />
      <LogViewer :lines="job.logs" empty-text="任务尚未产生日志" @clear="clearLogs" />

      <template v-if="resultText">
        <div class="divider" />
        <div class="stack-sm">
          <span class="label">执行结果</span>
          <pre class="result">{{ resultText }}</pre>
        </div>
      </template>

      <p v-if="jobRunning" class="hint" style="margin-top: 12px">
        任务在后台执行，可以自由切换页面；完成后会通过提示告知。
      </p>
    </UiCard>

    <UiCard v-else>
      <UiEmpty icon="list" title="当前没有任务" desc="到「操作台」选择要执行的操作，进度会显示在这里。">
        <RouterLink to="/ops" class="btn btn-primary btn-sm">去操作台</RouterLink>
      </UiEmpty>
    </UiCard>

    <UiCard title="最近任务" subtitle="仅保存在本机浏览器，最多 20 条">
      <div v-if="jobs.history.length" class="list">
        <div v-for="item in jobs.history" :key="item.id" class="list-row">
          <UiBadge :tone="historyTone(item)" :label="historyLabel(item)" dot />
          <div class="list-main">
            <span class="list-title">{{ ACTION_LABELS[item.action] || item.label }}</span>
            <span class="list-meta">{{ item.summary }} · {{ item.ref }}</span>
          </div>
          <div class="list-meta nowrap text-right">
            <div>{{ fmtTime(item.startedAt) }}</div>
            <div>{{ historyDuration(item) }}</div>
          </div>
        </div>
      </div>
      <UiEmpty v-else icon="clock" title="还没有任务历史" desc="执行一次操作后，这里会保留最近的执行记录。" />
    </UiCard>
  </div>
</template>
