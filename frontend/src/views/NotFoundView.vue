<script setup lang="ts">
import { RouterLink } from 'vue-router'
import PageHeader from '@/components/PageHeader.vue'
import UiCard from '@/components/UiCard.vue'
import UiEmpty from '@/components/UiEmpty.vue'
</script>

<template>
  <PageHeader title="页面不存在" desc="链接可能已失效，或地址输入有误" />
  <UiCard>
    <UiEmpty icon="alert" title="找不到这个页面" desc="返回概览继续使用控制台。">
      <RouterLink to="/" class="btn btn-primary btn-sm">回到概览</RouterLink>
    </UiEmpty>
  </UiCard>
</template>
