<script setup lang="ts">
defineProps<{ title?: string; subtitle?: string }>()
</script>

<template>
  <section class="card">
    <header v-if="title || $slots.actions" class="card-head">
      <div>
        <h3 v-if="title" class="card-title">{{ title }}</h3>
        <p v-if="subtitle" class="card-sub">{{ subtitle }}</p>
      </div>
      <div v-if="$slots.actions" class="card-actions">
        <slot name="actions" />
      </div>
    </header>
    <div class="card-body">
      <slot />
    </div>
  </section>
</template>
