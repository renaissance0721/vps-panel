// Render application components without testing Naive UI's teleport/animation internals.
import { defineComponent, h } from 'vue'

function container(name) {
  return defineComponent({
    name,
    setup: (_, { slots, attrs }) => () => h('section', attrs, [slots.header?.(), slots['header-extra']?.(), slots.default?.()]),
  })
}

export const NCard = container('NCard')
export const NAlert = container('NAlert')
export const NTag = container('NTag')
export const NEmpty = container('NEmpty')
export const NSpin = container('NSpin')
export const NProgress = container('NProgress')
export const NConfigProvider = container('NConfigProvider')
export const NModal = defineComponent({
  inheritAttrs: false,
  props: { show: Boolean },
  setup: (props, { slots }) => () => props.show ? slots.default?.() : null,
})
export const NDrawer = NModal
export const NDrawerContent = defineComponent({
  setup: (_, { slots, attrs }) => () => h('section', attrs, [slots.default?.(), slots.footer?.()]),
})
export const NButton = defineComponent({
  props: ['attrType'],
  setup: (props, { slots, attrs }) => () => h('button', { ...attrs, type: props.attrType }, slots.default?.()),
})
export const NInput = defineComponent({
  props: ['value'],
  setup: (props, { attrs }) => () => h('input', { ...attrs, value: props.value }),
})
