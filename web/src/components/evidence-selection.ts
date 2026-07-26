export interface EvidenceSelection {
  resultIds: string[]
  context?: string
}

export function evidenceSelection(
  resultIds: string[],
  context?: string
): EvidenceSelection | undefined {
  const deduped = dedupeResultIds(resultIds)
  return deduped.length === 0 ? undefined : { resultIds: deduped, context }
}

export function dedupeResultIds(resultIds: string[]): string[] {
  return [...new Set(resultIds.filter((id) => id.length > 0))]
}
