// Offset resolution for a rendered answer, kept apart from the renderer so both
// the answer and the analysis rail can ask the same questions of the same text.
//
// `url_citation` annotations arrive as [start, end) character indices into the
// raw markdown response (05), and mentions are located by searching that same
// string, so every range in here is in *source* coordinates. The renderer
// intersects them with each mdast text node's own source window.
import type {
  ResultAnalysis,
  ResultCitation,
  ResultMention,
} from "@/gen/opensight/v1/result_pb"

export interface MentionRange {
  start: number
  end: number
  mention: ResultMention
  // index into analysis.mentions — the rail's anchor key.
  index: number
}

export interface CitationRange {
  start: number
  end: number
  citation: ResultCitation
}

// mentionAnchorIndices reports which of `analysis.mentions` were actually
// located in the answer, so the rail only offers to scroll to the ones that
// have somewhere to scroll to.
export function mentionAnchorIndices(
  text: string,
  analysis: ResultAnalysis | undefined
): Set<number> {
  const anchors = new Set<number>()
  for (const range of mentionRanges(text, analysis?.mentions ?? [])) {
    anchors.add(range.index)
  }
  return anchors
}

export function mentionRanges(
  text: string,
  mentions: ResultMention[]
): MentionRange[] {
  const ranges: MentionRange[] = []
  mentions.forEach((mention, index) => {
    const range =
      findTextRange(text, mention.verbatimName) ??
      findTextRange(text, mention.excerpt)
    if (range !== null && range.end > range.start) {
      ranges.push({ ...range, mention, index })
    }
  })
  return ranges.sort((a, b) => a.start - b.start || a.end - b.end)
}

export function citationRanges(
  text: string,
  citations: ResultCitation[]
): CitationRange[] {
  const ranges: CitationRange[] = []
  for (const citation of citations) {
    if (citation.span === undefined) continue
    const start = citation.span.start
    const end = citation.span.end
    if (start < 0 || end <= start || start >= text.length) continue
    ranges.push({ start, end: Math.min(end, text.length), citation })
  }
  return ranges.sort(
    (a, b) => a.start - b.start || byCiteOrder(a.citation, b.citation)
  )
}

// findTextRange locates a mention's verbatim name, falling back to its excerpt,
// and finally to stitching an elided ("a … b") excerpt back together — the
// analysis records what the model wrote, which need not be byte-identical to
// the answer it was extracted from.
function findTextRange(
  text: string,
  needle: string
): { start: number; end: number } | null {
  const clean = needle.trim()
  if (clean.length === 0) return null

  const direct = text.indexOf(clean)
  if (direct >= 0) return { start: direct, end: direct + clean.length }

  const lowerText = text.toLowerCase()
  const lowerClean = clean.toLowerCase()
  const insensitive = lowerText.indexOf(lowerClean)
  if (insensitive >= 0) {
    return { start: insensitive, end: insensitive + clean.length }
  }

  const chunks = clean
    .split(/(?:\.{3}|…)/)
    .map((chunk) => chunk.trim())
    .filter((chunk) => chunk.length >= 3)
  if (chunks.length < 2) return null

  let cursor = 0
  let start = -1
  let end = -1
  for (const chunk of chunks) {
    const found = lowerText.indexOf(chunk.toLowerCase(), cursor)
    if (found < 0) return null
    if (start < 0) start = found
    end = found + chunk.length
    cursor = end
  }
  return start >= 0 && end > start ? { start, end } : null
}

export function byCiteOrder(a: ResultCitation, b: ResultCitation): number {
  return a.citeOrder - b.citeOrder
}

// withoutQuery strips the tracking params and trailing slash OpenAI appends to
// cited URLs, which differ between a citation record and the same URL as it
// appears inline in the prose.
export function withoutQuery(url: string): string {
  const cut = url.search(/[?#]/)
  const base = cut === -1 ? url : url.slice(0, cut)
  return base.endsWith("/") ? base.slice(0, -1) : base
}
