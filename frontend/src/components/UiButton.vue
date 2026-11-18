<script setup lang="ts">
import type { IconName } from '@/lib/icons'
import UiIcon from './UiIcon.vue'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'

withDefaults(
  defineProps<{
    variant?: Variant
    size?: 'md' | 'sm'
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    block?: boolean
    icon?: IconName
  }>(),
  { variant: 'secondary', size: 'md', type: 'button', disabled: false, block: false }
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="btn"
    :class="[`btn-${variant}`, size === 'sm' ? 'btn-sm' : '', block ? 'btn-block' : '']"
  >
    <UiIcon v-if="icon" :name="icon" :size="size === 'sm' ? 14 : 16" />
    <slot />
  </button>
</template>
