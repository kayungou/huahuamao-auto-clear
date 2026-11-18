<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { jobs, jobRunning } from '@/stores/jobs'
import UiBadge from './UiBadge.vue'
import UiProgress from './UiProgress.vue'

const job = computed(() => jobs.job)

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

const meta = computed(() => {
  const j = job.value
  if (!j) return ''
  if (j.total > 0) return `${j.cur} / ${j.total}`
  return j.phase || ''
})
</script>

<template>
  <div class="card">
    <div class="card-body">
      <div class="job-head">
        <div class="row" style="gap: 8px">
          <span class="job-phase">{{ job?.phase || '当前没有运行中的任务' }}</span>
          <UiBadge :tone="tone" :label="statusText" dot />
        </div>
        <RouterLink class="text-sm" to="/jobs">查看日志</RouterLink>
      </div>
      <UiProgress v-if="job" :value="job.cur" :max="job.total" :hint="meta" />
      <p v-if="!job" class="hint">在操作台发起任务后，这里会实时显示进度。</p>
      <p v-else-if="jobRunning" class="hint" style="margin-top: 10px">
        任务在后台执行，可以切换到其他页面，完成后会提示。
      </p>
    </div>
  </div>
</template>
