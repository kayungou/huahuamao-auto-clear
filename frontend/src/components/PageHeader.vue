<script setup lang="ts">
defineProps<{ title: string; desc?: string }>()
</script>

<template>
  <header class="page-head">
    <div>
      <h1 class="page-title">{{ title }}</h1>
      <p v-if="desc" class="page-desc">{{ desc }}</p>
    </div>
    <div v-if="$slots.actions" class="page-actions">
      <slot name="actions" />
    </div>
  </header>
</template>
