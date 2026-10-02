let activePreview: HTMLElement | null = null
let activeSource: HTMLElement | null = null
let scrollTarget: HTMLElement | null = null
let pointer: { x: number; y: number } | null = null
let overElement: Element | null = null
let frame: number | null = null
let sourceObserver: MutationObserver | null = null

const AUTO_SCROLL_EDGE = 72

export function edgeScrollVelocity(y: number, top: number, bottom: number, scrollTop: number, maxScroll: number) {
  if (![y, top, bottom, scrollTop, maxScroll].every(Number.isFinite) || bottom <= top || y < top || y > bottom) return 0
  const edge = Math.min(AUTO_SCROLL_EDGE, (bottom - top) / 2)
  if (y < top + edge && scrollTop > 0) {
    return -Math.min(scrollTop, 3 + 17 * (1 - (y - top) / edge))
  }
  if (y > bottom - edge && scrollTop < maxScroll) {
    return Math.min(maxScroll - scrollTop, 3 + 17 * (1 - (bottom - y) / edge))
  }
  return 0
}

export function nearestScrollableAncestor(source: HTMLElement): HTMLElement {
  for (let parent = source.parentElement; parent; parent = parent.parentElement) {
    if (parent.scrollHeight > parent.clientHeight && /^(auto|scroll|overlay)$/.test(getComputedStyle(parent).overflowY)) return parent
  }
  return (document.scrollingElement ?? document.documentElement) as HTMLElement
}

function scheduleFrame() {
  if (frame !== null) return
  frame = requestAnimationFrame(() => {
    frame = null
    activePreview?.remove()
    activePreview = null
    if (!activeSource?.isConnected) {
      endDragPreview()
      return
    }
    if (!pointer || !scrollTarget) return
    const isPage = scrollTarget === document.scrollingElement || scrollTarget === document.documentElement
    const bounds = isPage
      ? { top: 0, bottom: window.innerHeight, left: 0, right: window.innerWidth }
      : scrollTarget.getBoundingClientRect()
    if (pointer.x < Math.max(0, bounds.left) || pointer.x > Math.min(window.innerWidth, bounds.right)) return
    const height = isPage ? window.innerHeight : scrollTarget.clientHeight
    const velocity = edgeScrollVelocity(pointer.y, Math.max(0, bounds.top), Math.min(window.innerHeight, bounds.bottom),
      scrollTarget.scrollTop, Math.max(0, scrollTarget.scrollHeight - height))
    if (velocity === 0) return
    const before = scrollTarget.scrollTop
    scrollTarget.scrollTop += velocity
    if (scrollTarget.scrollTop !== before) {
      // Native dragover may wait for another mouse move after scrolling.
      // Refresh hover only; the real drop remains responsible for reordering.
      const target = document.elementFromPoint(pointer.x, pointer.y)
      if (target !== overElement) {
        overElement?.dispatchEvent(new DragEvent('dragleave', { bubbles: true, relatedTarget: target }))
        overElement = target
      }
      target?.dispatchEvent(new DragEvent('dragover', {
        bubbles: true, cancelable: true, clientX: pointer.x, clientY: pointer.y,
      }))
      scheduleFrame()
    }
  })
}

function trackDrag(event: DragEvent) {
  pointer = { x: event.clientX, y: event.clientY }
  overElement = event.target as Element | null
  scheduleFrame()
}

function leaveDocument(event: DragEvent) {
  if (event.isTrusted && event.relatedTarget === null) pointer = null
}

function cancelWithEscape(event: KeyboardEvent) {
  if (event.key === 'Escape') endDragPreview()
}

export function beginDragPreview(event: DragEvent, source: HTMLElement, payload: string) {
  if (!event.dataTransfer) return false
  endDragPreview()

  const bounds = source.getBoundingClientRect()
  const clone = source.cloneNode(true) as HTMLElement
  let preview = clone
  if (source instanceof HTMLTableRowElement) {
    const table = document.createElement('table')
    table.className = `${source.closest('table')?.className ?? ''} drag-preview`
    const body = document.createElement('tbody')
    body.append(clone)
    table.append(body)
    preview = table
  } else {
    preview.classList.add('drag-preview')
  }
  preview.style.width = `${bounds.width}px`
  preview.style.top = `${bounds.top}px`
  preview.style.left = `${bounds.left}px`
  document.body.append(preview)

  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', payload)
  event.dataTransfer.setDragImage(
    preview,
    Math.max(0, Math.min(bounds.width, event.clientX - bounds.left)),
    Math.max(0, Math.min(bounds.height, event.clientY - bounds.top)),
  )
  source.classList.add('drag-source')
  activePreview = preview
  activeSource = source
  scrollTarget = nearestScrollableAncestor(source)
  pointer = { x: event.clientX, y: event.clientY }
  document.addEventListener('dragover', trackDrag, { capture: true })
  document.addEventListener('dragleave', leaveDocument, { capture: true })
  document.addEventListener('dragend', endDragPreview, { capture: true })
  document.addEventListener('drop', endDragPreview, { capture: true })
  document.addEventListener('keydown', cancelWithEscape, { capture: true })
  window.addEventListener('blur', endDragPreview)
  sourceObserver = new MutationObserver(() => {
    if (!source.isConnected) endDragPreview()
  })
  sourceObserver.observe(document.body, { childList: true, subtree: true })
  scheduleFrame()
  return true
}

export function endDragPreview() {
  if (!activeSource) return
  if (frame !== null) cancelAnimationFrame(frame)
  frame = null
  pointer = null
  overElement = null
  scrollTarget = null
  sourceObserver?.disconnect()
  sourceObserver = null
  document.removeEventListener('dragover', trackDrag, { capture: true })
  document.removeEventListener('dragleave', leaveDocument, { capture: true })
  document.removeEventListener('dragend', endDragPreview, { capture: true })
  document.removeEventListener('drop', endDragPreview, { capture: true })
  document.removeEventListener('keydown', cancelWithEscape, { capture: true })
  window.removeEventListener('blur', endDragPreview)
  activeSource?.classList.remove('drag-source')
  activePreview?.remove()
  activeSource = null
  activePreview = null
}
