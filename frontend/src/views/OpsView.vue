<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { ActionKey } from '@/api/types'
import { ACTION_LABELS, jobRunning, startJob } from '@/stores/jobs'
import { prefs } from '@/stores/prefs'
import { session } from '@/stores/session'
import { confirmDialog, toast } from '@/stores/ui'
import JobRunner from '@/components/JobRunner.vue'
import PageHeader from '@/components/PageHeader.vue'
import UiButton from '@/components/UiButton.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
import UiInput from '@/components/UiInput.vue'

const RANK_TYPES = [
  { value: 'stars', label: '星星榜' },
  { value: 'cleared', label: '通关榜' },
  { value: 'daily', label: '每日榜' },
  { value: 'endless', label: '冰原 / 无尽' },
  { value: 'match', label: '消消喵' },
  { value: 'sudoku', label: '数独喵' }
]

async function confirmRun(
  action: ActionKey,
  params: Record<string, unknown>,
  message: string,
  danger = false
): Promise<void> {
  if (jobRunning.value) {
    toast('已有任务在执行中，请等待完成', 'error')
    return
  }
  const ok = await confirmDialog({
    title: `执行「${ACTION_LABELS[action]}」？`,
    message,
    confirmText: '开始执行',
    danger
  })
  if (!ok) return
  await startJob(action, params)
}

async function runClear(): Promise<void> {
  if (prefs.clearTo < prefs.clearFrom) {
    toast('结束关必须大于等于起始关', 'error')
    return
  }
  await confirmRun(
    'clear',
    { from: prefs.clearFrom, to: prefs.clearTo },
    `将把第 ${prefs.clearFrom} 关到第 ${prefs.clearTo} 关按顺序刷到三星。服务端进度只增不减，请确认区间无误。`,
    true
  )
}

async function runSudoku(): Promise<void> {
  await confirmRun('sudoku', {}, '将自动完成数独喵的 200 关，并把每关星级补到三星。', true)
}

async function runFish(): Promise<void> {
  await confirmRun('fish', {}, '将收集「摸鱼」模式中所有可收集的小鱼。', true)
}

async function runEndless(): Promise<void> {
  await confirmRun('endless', { floor: prefs.endlessFloor }, `将把无尽模式推进到第 ${prefs.endlessFloor} 层。`, true)
}

async function runMatch(): Promise<void> {
  await confirmRun(
    'match',
    { best: prefs.matchBest },
    `将把消消喵推进到第 ${prefs.matchBest} 关。服务端要求逐关递增，程序会自动分批提交。`,
    true
  )
}

async function runItems(): Promise<void> {
  await confirmRun(
    'items',
    { payload: { hint: 999, can: 999, map: 999, pass: 999 } },
    '将把提示、撤回、地图、跳关四种道具补到 999。',
    true
  )
}

async function runAdlog(): Promise<void> {
  await confirmRun('adlog', { count: prefs.adCount }, `将写入 ${prefs.adCount} 条广告观看记录。`, true)
}

async function runRank(): Promise<void> {
  if (jobRunning.value) {
    toast('已有任务在执行中，请等待完成', 'error')
    return
  }
  await startJob('rank', { type: prefs.rankType })
}

async function runConfig(): Promise<void> {
  if (jobRunning.value) {
    toast('已有任务在执行中，请等待完成', 'error')
    return
  }
  await startJob('config', {})
}
</script>

<template>
  <PageHeader title="操作台" desc="所有写操作都会先确认；执行进度见「任务与日志」" />

  <div v-if="!session.token" class="stack">
    <UiCard>
      <UiEmpty icon="user" title="请先扫码登录" desc="Token 保存在本机 Cookie，操作台会用它直接调用游戏接口。">
        <RouterLink to="/scan" class="btn btn-primary btn-sm">去扫码登录</RouterLink>
      </UiEmpty>
    </UiCard>
  </div>

  <div v-else class="stack">
    <JobRunner />

    <UiCard title="主关卡" subtitle="按顺序推进，服务端只增不减">
      <div class="form-row">
        <UiInput v-model="prefs.clearFrom" label="起始关" type="number" :min="1" :max="9999" />
        <UiInput v-model="prefs.clearTo" label="结束关" type="number" :min="1" :max="9999" />
        <UiButton variant="primary" icon="play" :disabled="jobRunning" @click="runClear">开始通关</UiButton>
      </div>
      <p class="hint" style="margin-top: 10px">
        每关会先探测最小步数再补到三星；已三星的关卡会被跳过。数量较大时请耐心等待任务完成。
      </p>
    </UiCard>

    <UiCard title="小游戏" subtitle="数独喵 / 摸鱼 / 无尽 / 消消喵">
      <div class="op-grid">
        <div class="op">
          <div class="op-title"><span>数独喵</span><span class="badge badge-idle">200 关</span></div>
          <p class="op-desc">一键完成全部 200 关，并把每关星级补到三星。</p>
          <div class="op-row">
            <UiButton variant="secondary" :disabled="jobRunning" @click="runSudoku">开始</UiButton>
          </div>
        </div>

        <div class="op">
          <div class="op-title"><span>摸鱼 / 小鱼</span></div>
          <p class="op-desc">收集「摸鱼」模式中所有可收集的小鱼。</p>
          <div class="op-row">
            <UiButton variant="secondary" :disabled="jobRunning" @click="runFish">开始</UiButton>
          </div>
        </div>

        <div class="op">
          <div class="op-title"><span>无尽模式</span></div>
          <p class="op-desc">把冰原无尽推进到指定层数。</p>
          <div class="op-row">
            <UiInput v-model="prefs.endlessFloor" label="目标层数" type="number" :min="1" :max="9999" />
            <UiButton variant="secondary" :disabled="jobRunning" @click="runEndless">开始</UiButton>
          </div>
        </div>

        <div class="op">
          <div class="op-title"><span>消消喵</span><span class="badge badge-idle">逐关递增</span></div>
          <p class="op-desc">服务端要求每关 +1，程序会自动分批提交并在被拒时重试。</p>
          <div class="op-row">
            <UiInput v-model="prefs.matchBest" label="目标关数" type="number" :min="1" :max="9999" />
            <UiButton variant="secondary" :disabled="jobRunning" @click="runMatch">开始</UiButton>
          </div>
        </div>
      </div>
    </UiCard>

    <UiCard title="道具与广告" subtitle="会直接修改服务端账号数据">
      <div class="op-grid">
        <div class="op">
          <div class="op-title"><span>道具补满</span></div>
          <p class="op-desc">提示、撤回、地图、跳关补到 999。</p>
          <div class="op-row">
            <UiButton variant="secondary" :disabled="jobRunning" @click="runItems">补满</UiButton>
          </div>
        </div>

        <div class="op">
          <div class="op-title"><span>广告记录</span></div>
          <p class="op-desc">写入指定条数的广告观看记录。</p>
          <div class="op-row">
            <UiInput v-model="prefs.adCount" label="条数" type="number" :min="1" :max="200" />
            <UiButton variant="secondary" :disabled="jobRunning" @click="runAdlog">写入</UiButton>
          </div>
        </div>
      </div>
    </UiCard>

    <UiCard title="查询" subtitle="只读，不修改数据">
      <div class="op-grid">
        <div class="op">
          <div class="op-title"><span>排行榜</span></div>
          <p class="op-desc">读取指定榜单前若干名。</p>
          <div class="op-row">
            <div class="field" style="flex: 1">
              <label class="label" for="rank-type">榜单</label>
              <select id="rank-type" v-model="prefs.rankType" class="select">
                <option v-for="item in RANK_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
              </select>
            </div>
            <UiButton variant="secondary" :disabled="jobRunning" @click="runRank">查询</UiButton>
          </div>
        </div>

        <div class="op">
          <div class="op-title"><span>配置 config</span></div>
          <p class="op-desc">读取服务端下发的活动、配额等配置。</p>
          <div class="op-row">
            <UiButton variant="secondary" :disabled="jobRunning" @click="runConfig">查询</UiButton>
          </div>
        </div>
      </div>
    </UiCard>

    <p class="hint">
      所有写操作只对已授权的测试账号使用。服务端进度只增不减，重复执行通常不会产生额外影响。
    </p>
  </div>
</template>
