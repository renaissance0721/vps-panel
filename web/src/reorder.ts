export function moveRow<T extends { id: number }>(rows: T[], draggedID: number, targetID: number) {
  const oldIndex = rows.findIndex((row) => row.id === draggedID)
  const newIndex = rows.findIndex((row) => row.id === targetID)
  if (oldIndex < 0 || newIndex < 0 || oldIndex === newIndex) return null
  const [row] = rows.splice(oldIndex, 1)
  rows.splice(newIndex, 0, row)
  return { direction: newIndex < oldIndex ? 'up' as const : 'down' as const, steps: Math.abs(newIndex - oldIndex) }
}

export async function persistMove(
  move: { direction: 'up' | 'down'; steps: number },
  sendStep: (direction: 'up' | 'down') => Promise<unknown>,
  reload: () => Promise<void>,
) {
  let failure: unknown = null
  try {
    for (let step = 0; step < move.steps; step++) await sendStep(move.direction)
  } catch (reason) {
    failure = reason
  }
  try {
    await reload()
  } catch (reason) {
    if (failure === null) failure = reason
  }
  if (failure !== null) throw failure
}
