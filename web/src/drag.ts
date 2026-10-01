let activePreview: HTMLElement | null = null
let activeSource: HTMLElement | null = null

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
  requestAnimationFrame(() => {
    if (activePreview !== preview) return
    preview.remove()
    activePreview = null
  })
  return true
}

export function endDragPreview() {
  activeSource?.classList.remove('drag-source')
  activePreview?.remove()
  activeSource = null
  activePreview = null
}
