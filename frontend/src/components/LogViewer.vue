<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import UiButton from './UiButton.vue'

const props = withDefaults(defineProps<{ lines: string[]; emptyText?: string }>(), {
  emptyText: '暂无日志'
})
const emit = defineEmits<{ clear: [] }>()

const box = ref<HTMLPreElement | null>(null)
const autoScroll = ref(true)

watch(
  () => props.lines.length,
  async () => {
    if (!autoScroll.value) return
    await nextTick()
    const el = box.value
    if (el) el.scrollTop = el.scrollHeight
  }
)
</script>

<template>
  <div class="stack-sm">
    <div class="row" style="justify-content: space-between">
      <label class="row text-sm muted" style="gap: 6px">
        <input v-model="autoScroll" type="checkbox" />
        自动滚动
      </label>
      <UiButton size="sm" variant="ghost" @click="emit('clear')">清空日志</UiButton>
    </div>
    <pre ref="box" class="log"><span v-for="(line, index) in lines" :key="index" class="log-line">{{ line }}</span><span v-if="!lines.length" class="log-empty">{{ emptyText }}</span></pre>
  </div>
</template>
