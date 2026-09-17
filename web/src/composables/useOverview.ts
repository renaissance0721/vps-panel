import {
  computed,
  ref,
  type Ref,
  type UnwrapNestedRefs,
} from 'vue'
import {
  api,
} from '../api/client'
import type {
  AuthState,
} from '../types/auth'
import type {
  Health,
  Invitation,
  Overview,
} from '../types/overview'

import {
  formatTime,
} from '../format'
export function useOverview(state: Ref<AuthState | null>, health: Ref<Health | null>, submitting: Ref<boolean>, error: Ref<string>, submit: (action: () => Promise<void>) => Promise<void>) {
  const invitations = ref<Invitation[]>([])
  const overview = ref<Overview | null>(null)
  const generatedLink = ref('')
  const copied = ref(false)

  const isHealthy = computed(
    () => health.value?.status === 'ok' && health.value.database === 'ok',
  )

  async function loadInvitations() {
    const response = await api<{ invitations: Invitation[] }>('/api/admin/invitations')
    invitations.value = response.invitations
  }

  async function loadOverview() {
    overview.value = await api<Overview>('/api/overview')
  }

  async function createInvitation() {
    await submit(async () => {
      const invitation = await api<Invitation>('/api/admin/invitations', { method: 'POST' })
      generatedLink.value = `${window.location.origin}/register?token=${encodeURIComponent(invitation.token ?? '')}`
      copied.value = false
      await loadInvitations()
    })
  }

  async function revokeInvitation(id: number) {
    await submit(async () => {
      await api(`/api/admin/invitations/${id}`, { method: 'DELETE' })
      await loadInvitations()
    })
  }

  async function copyInvitation() {
    try {
      await navigator.clipboard.writeText(generatedLink.value)
      copied.value = true
    } catch {
      error.value = '无法自动复制，请手动复制邀请链接'
    }
  }
  function resetSession() { invitations.value = []; overview.value = null; generatedLink.value = '' }
  return {
    invitations,
    overview,
    generatedLink,
    copied,
    isHealthy,
    loadOverview,
    loadInvitations,
    createInvitation,
    revokeInvitation,
    copyInvitation,
    state,
    health,
    submitting,
    formatTime,
    resetSession,
  }
}
export type OverviewViewState = UnwrapNestedRefs<ReturnType<typeof useOverview>>
