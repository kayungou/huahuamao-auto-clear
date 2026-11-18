<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { initSession, startHealthPolling } from '@/stores/session'
import { stopPolling } from '@/stores/jobs'
import SideNav from './SideNav.vue'
import TopBar from './TopBar.vue'
import TabBar from './TabBar.vue'
import UiDialog from './UiDialog.vue'
import UiToast from './UiToast.vue'

let stopHealth: (() => void) | null = null

onMounted(() => {
  void initSession()
  stopHealth = startHealthPolling()
})

onBeforeUnmount(() => {
  stopHealth?.()
  stopPolling()
})
</script>

<template>
  <div class="shell">
    <SideNav />
    <div class="main">
      <TopBar />
      <main id="content" class="page">
        <RouterView />
      </main>
      <TabBar />
    </div>
    <UiDialog />
    <UiToast />
  </div>
</template>
