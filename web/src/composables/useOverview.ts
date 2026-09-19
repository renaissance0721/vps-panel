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
  const backupFile = ref<File | null>(null)
  const backupBusy = ref(false)
  const backupStatus = ref('')

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

  function selectBackup(event: Event) {
    backupFile.value = (event.target as HTMLInputElement).files?.[0] ?? null
    backupStatus.value = ''
  }

  async function exportBackup() {
    backupBusy.value = true
    error.value = ''
    try {
      const response = await fetch('/api/admin/backup/export', { credentials: 'same-origin', cache: 'no-store' })
      if (!response.ok) {
        const body = await response.json().catch(() => null) as { error?: string } | null
        throw new Error(body?.error ?? `导出失败（${response.status}）`)
      }
      const url = URL.createObjectURL(await response.blob())
      const link = document.createElement('a')
      link.href = url
      link.download = `vps-panel-backup-${new Date().toISOString().replace(/[-:]/g, '').slice(0, 15)}.zip`
      document.body.append(link)
      link.click()
      link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 60_000)
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '导出失败'
    } finally {
      backupBusy.value = false
    }
  }

  async function importBackup() {
    if (!backupFile.value) return
    if (!window.confirm('导入备份会完全覆盖当前 Panel 的所有数据，并重启 Panel。当前面板中不属于备份的用户、服务器、节点及历史数据都会被删除。是否继续？')) return
    if (window.prompt('请输入 RESTORE 确认完整覆盖恢复') !== 'RESTORE') return
    backupBusy.value = true
    error.value = ''
    backupStatus.value = ''
    try {
      const form = new FormData()
      form.append('backup', backupFile.value)
      form.append('confirmation', 'RESTORE')
      const response = await fetch('/api/admin/backup/import', {
        method: 'POST', credentials: 'same-origin', cache: 'no-store', body: form,
      })
      if (!response.ok) {
        const body = await response.json().catch(() => null) as { error?: string } | null
        throw new Error(body?.error ?? `导入失败（${response.status}）`)
      }
      backupStatus.value = '备份已验证，Panel 正在恢复并重启……'
      const deadline = Date.now() + 90_000
      while (Date.now() < deadline) {
        await new Promise((resolve) => window.setTimeout(resolve, 2_000))
        try {
          const health = await fetch('/api/health', { credentials: 'same-origin', cache: 'no-store' })
          if (health.ok) {
            window.location.reload()
            return
          }
        } catch { /* Panel is restarting. */ }
      }
      backupStatus.value = 'Panel 尚未恢复在线，请检查服务状态。'
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '导入失败'
    } finally {
      backupBusy.value = false
    }
  }
  function resetSession() { invitations.value = []; overview.value = null; generatedLink.value = ''; backupFile.value = null; backupStatus.value = '' }
  return {
    invitations,
    overview,
    generatedLink,
    copied,
    backupFile,
    backupBusy,
    backupStatus,
    selectBackup,
    exportBackup,
    importBackup,
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
