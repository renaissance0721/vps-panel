import type { User } from './types/auth'

export type AdminPage = 'overview' | 'monitor' | 'servers' | 'proxies' | 'relays' | 'subscriptions' | 'accounts' | 'carpool' | 'settings' | 'audit'
export type SubscriptionTab = 'personal' | 'remote' | 'users' | 'plans' | 'nodes' | 'configuration'
type ManagementRole = Extract<User['role'], 'admin' | 'vip'>
export type NavigationRoute = { page: AdminPage; tab: SubscriptionTab }

const pages: AdminPage[] = ['overview', 'monitor', 'servers', 'proxies', 'relays', 'subscriptions', 'accounts', 'carpool', 'settings', 'audit']
const adminPages: AdminPage[] = ['accounts', 'carpool', 'settings', 'audit']
const tabs: SubscriptionTab[] = ['personal', 'remote', 'users', 'plans', 'nodes', 'configuration']

export function resolveRoute(hash: string, role: ManagementRole): NavigationRoute {
  const [path, query = ''] = hash.slice(1).split('?')
  const requestedPage = path.startsWith('/') ? path.slice(1) : ''
  const page = pages.find((item) => item === requestedPage)
  if (!page || (role !== 'admin' && adminPages.includes(page))) return { page: 'overview', tab: 'personal' }
  const requestedTab = new URLSearchParams(query).get('tab')
  const tab = tabs.find((item) => item === requestedTab)
  return { page, tab: page === 'subscriptions' && tab && (role === 'admin' || tab === 'personal' || tab === 'remote') ? tab : 'personal' }
}

export function routeHash(route: NavigationRoute): string {
  return route.page === 'subscriptions' ? `#/subscriptions?tab=${route.tab}` : `#/${route.page}`
}

// Only management sessions own the hash; invitation and email paths retain their queries.
export function createNavigation(browser: Window, apply: (route: NavigationRoute, refresh: boolean) => void) {
  let role: ManagementRole | null = null

  function sync(refresh = true) {
    if (!role || browser.location.pathname !== '/') return
    const route = resolveRoute(browser.location.hash, role)
    const hash = routeHash(route)
    if (browser.location.hash !== hash) browser.history.replaceState(browser.history.state, '', hash)
    apply(route, refresh)
  }

  function onHistoryChange() { sync() }
  browser.addEventListener('popstate', onHistoryChange)
  browser.addEventListener('hashchange', onHistoryChange)

  return {
    authenticate(nextRole: User['role'] | undefined) {
      role = nextRole === 'admin' || nextRole === 'vip' ? nextRole : null
      sync(false)
    },
    navigate(route: NavigationRoute) {
      if (!role || browser.location.pathname !== '/') return
      const safeRoute = resolveRoute(routeHash(route), role)
      const hash = routeHash(safeRoute)
      if (browser.location.hash !== hash) browser.history.pushState(browser.history.state, '', hash)
      apply(safeRoute, true)
    },
    logout() {
      role = null
      if (browser.location.pathname === '/') browser.history.replaceState(browser.history.state, '', '#/overview')
      apply({ page: 'overview', tab: 'personal' }, false)
    },
    dispose() {
      browser.removeEventListener('popstate', onHistoryChange)
      browser.removeEventListener('hashchange', onHistoryChange)
    },
  }
}
