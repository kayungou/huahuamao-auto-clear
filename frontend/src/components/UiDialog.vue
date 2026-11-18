<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { settleDialog, ui } from '@/stores/ui'
import UiButton from './UiButton.vue'

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && ui.dialog) settleDialog(false)
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div v-if="ui.dialog" class="dialog-mask" @click.self="settleDialog(false)">
    <div class="dialog" role="dialog" aria-modal="true" :aria-label="ui.dialog.title">
      <div class="dialog-head">{{ ui.dialog.title }}</div>
      <div v-if="ui.dialog.message" class="dialog-body">{{ ui.dialog.message }}</div>
      <div class="dialog-foot">
        <UiButton variant="secondary" @click="settleDialog(false)">{{ ui.dialog.cancelText }}</UiButton>
        <UiButton :variant="ui.dialog.danger ? 'danger' : 'primary'" @click="settleDialog(true)">
          {{ ui.dialog.confirmText }}
        </UiButton>
      </div>
    </div>
  </div>
</template>
