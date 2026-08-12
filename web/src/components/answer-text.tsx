// The rendered answer: the model's markdown response, with self/competitor
// mentions highlighted and its citations turned into numbered chips.
//
// The whole difficulty here is offsets. `url_citation` annotations arrive as
// [start, end) character indices into the *raw markdown source* (05), and
// mentions are located by searching that same source. So the markdown cannot be
// rendered by anything that discards source positions — the highlights would
// land on the wrong characters. mdast carries `position.*.offset` on every
// node, so ranges are computed once against the source and then intersected
// with each text node's own offset window as it renders.
import type {
  Blockquote,
  Code,
  Heading,
  InlineCode,
  Link,
  List,
  ListItem,
  Paragraph,
  PhrasingContent,
  RootContent,
  Text,
} from "mdast"
import { fromMarkdown } from "mdast-util-from-markdown"
import { gfmFromMarkdown } from "mdast-util-gfm"
import { gfm } from "micromark-extension-gfm"
import { type ReactNode, useMemo } from "react"

import { mentionSubjectLabel } from "@/api/labels"
import { MentionSubject } from "@/gen/opensight/v1/common_pb"
import type {
  ResultAnalysis,
  ResultCitation,
  ResultMention,
} from "@/gen/opensight/v1/result_pb"
import {
  byCiteOrder,
  citationRanges,
  mentionRanges,
  withoutQuery,
  type CitationRange,
  type MentionRange,
} from "@/lib/answer-ranges"
import { cn } from "@/lib/utils"

// AnswerHighlight is the element the reader just selected in the analysis rail.
// It flashes a ring so the eye can find it in a long answer.
export type AnswerHighlight =
  | { kind: "citation"; citeOrder: number }
  | { kind: "mention"; index: number }

export function AnswerText({
  text,
  analysis,
  active,
}: {
  text: string
  analysis: ResultAnalysis | undefined
  active?: AnswerHighlight
}) {
  const parsed = useMemo(() => parseAnswer(text, analysis), [text, analysis])
  const ctx: RenderContext = { ...parsed, active }
  return (
    <div className="flex flex-col gap-4 text-sm leading-7 text-foreground">
      {renderBlocks(parsed.blocks, ctx)}
    </div>
  )
}

interface ParsedAnswer {
  blocks: RootContent[]
  mentions: MentionRange[]
  citations: CitationRange[]
  // Citations with no markdown link of their own, keyed by the source offset
  // their span ends at: these get a trailing superscript, since nothing else in
  // the text will carry their number.
  markersByEnd: Map<number, ResultCitation[]>
  citationByUrl: Map<string, ResultCitation>
}

interface RenderContext extends ParsedAnswer {
  active: AnswerHighlight | undefined
}

function parseAnswer(
  text: string,
  analysis: ResultAnalysis | undefined
): ParsedAnswer {
  const tree = fromMarkdown(text, {
    extensions: [gfm()],
    mdastExtensions: [gfmFromMarkdown()],
  })

  const citations = analysis?.citations ?? []
  const citationByUrl = new Map<string, ResultCitation>()
  for (const citation of citations) {
    citationByUrl.set(citation.url, citation)
    const bare = withoutQuery(citation.url)
    if (!citationByUrl.has(bare)) citationByUrl.set(bare, citation)
  }

  // A citation whose URL appears as a link in the prose already has a place to
  // hang its number; only the rest need a synthesised trailing marker.
  const linked = new Set<number>()
  visitLinks(tree.children, (node) => {
    const citation = matchCitation(citationByUrl, node.url)
    if (citation) linked.add(citation.citeOrder)
  })

  const citationSpans = citationRanges(text, citations)
  const markersByEnd = new Map<number, ResultCitation[]>()
  for (const range of citationSpans) {
    if (linked.has(range.citation.citeOrder)) continue
    const markers = markersByEnd.get(range.end) ?? []
    markers.push(range.citation)
    markersByEnd.set(range.end, markers)
  }
  for (const markers of markersByEnd.values()) markers.sort(byCiteOrder)

  return {
    blocks: tree.children,
    mentions: mentionRanges(text, analysis?.mentions ?? []),
    citations: citationSpans,
    markersByEnd,
    citationByUrl,
  }
}

function renderBlocks(nodes: RootContent[], ctx: RenderContext): ReactNode {
  return nodes.map((node, index) => renderBlock(node, index, ctx))
}

function renderBlock(
  node: RootContent,
  key: number,
  ctx: RenderContext
): ReactNode {
  switch (node.type) {
    case "paragraph":
      return <p key={key}>{renderInline((node as Paragraph).children, ctx)}</p>
    case "heading": {
      const heading = node as Heading
      return (
        <p
          key={key}
          className={cn(
            "font-heading font-semibold text-foreground",
            heading.depth <= 2 ? "text-base" : "text-sm"
          )}
        >
          {renderInline(heading.children, ctx)}
        </p>
      )
    }
    case "list": {
      const list = node as List
      const items = list.children.map((item, index) => (
        <li key={index} className="ps-1">
          {renderBlocks((item as ListItem).children, ctx)}
        </li>
      ))
      return list.ordered ? (
        <ol key={key} className="flex list-decimal flex-col gap-2 ps-5">
          {items}
        </ol>
      ) : (
        <ul key={key} className="flex list-disc flex-col gap-2 ps-5">
          {items}
        </ul>
      )
    }
    case "blockquote":
      return (
        <blockquote
          key={key}
          className="flex flex-col gap-2 border-s-2 border-border ps-4 text-muted-foreground"
        >
          {renderBlocks((node as Blockquote).children, ctx)}
        </blockquote>
      )
    case "code":
      return (
        <pre
          key={key}
          className="overflow-x-auto rounded-lg bg-muted p-3 text-xs leading-5"
        >
          {(node as Code).value}
        </pre>
      )
    case "thematicBreak":
      return <hr key={key} className="border-border" />
    default:
      // Anything else (tables, HTML, footnotes) renders as its own children
      // when it has them, and is dropped when it does not — the answer is
      // prose, and an unrenderable node is not worth failing the page over.
      return "children" in node ? (
        <div key={key} className="flex flex-col gap-4">
          {renderBlocks(node.children as RootContent[], ctx)}
        </div>
      ) : null
  }
}

function renderInline(
  nodes: PhrasingContent[],
  ctx: RenderContext
): ReactNode[] {
  const trims = citationParenTrims(nodes, ctx)
  return nodes
    .map((node, index) =>
      trims.get(index) === "drop"
        ? null
        : renderPhrase(node, index, ctx, trims.get(index))
    )
    .filter((node) => node !== null)
}

// The model writes its citations as a parenthetical of bare links —
// "…recognised disease. ([endooffices.com](https://…?utm_source=openai))".
// Once each link has become its numbered chip the surrounding punctuation is
// left stranded as "disease. ( 1 )", so a parenthesis whose entire contents
// turned into chips is dropped along with them.
type Trim = { head: number; tail: number } | "drop"

function citationParenTrims(
  nodes: PhrasingContent[],
  ctx: RenderContext
): Map<number, Trim> {
  const trims = new Map<number, Trim>()
  const isChip = (node: PhrasingContent) =>
    node.type === "link" && matchCitation(ctx.citationByUrl, node.url) !== undefined
  // Text that merely separates citations inside the parenthetical.
  const isSeparator = (node: PhrasingContent) =>
    node.type === "text" && /^[\s,;]*(and)?[\s,;]*$/.test(node.value)

  for (let open = 0; open < nodes.length; open++) {
    const node = nodes[open]
    if (node.type !== "text" || !/\(\s*$/.test(node.value)) continue

    let chips = 0
    let cursor = open + 1
    while (cursor < nodes.length) {
      if (isChip(nodes[cursor])) chips++
      else if (!isSeparator(nodes[cursor])) break
      cursor++
    }
    const close = nodes[cursor]
    if (chips === 0 || close === undefined) continue
    if (close.type !== "text" || !/^\s*\)/.test(close.value)) continue

    trims.set(open, { head: 0, tail: /\s*\(\s*$/.exec(node.value)![0].length })
    for (let i = open + 1; i < cursor; i++) {
      if (!isChip(nodes[i])) trims.set(i, "drop")
    }
    trims.set(cursor, {
      head: /^\s*\)/.exec(close.value)![0].length,
      tail: 0,
    })
    open = cursor
  }
  return trims
}

function renderPhrase(
  node: PhrasingContent,
  key: number,
  ctx: RenderContext,
  trim?: Trim
): ReactNode {
  switch (node.type) {
    case "text":
      return renderText(node as Text, key, ctx, trim)
    case "strong":
      return (
        <strong key={key} className="font-semibold">
          {renderInline(node.children, ctx)}
        </strong>
      )
    case "emphasis":
      return (
        <em key={key} className="italic">
          {renderInline(node.children, ctx)}
        </em>
      )
    case "delete":
      return (
        <del key={key} className="line-through">
          {renderInline(node.children, ctx)}
        </del>
      )
    case "inlineCode":
      return (
        <code
          key={key}
          className="rounded bg-muted px-1 py-0.5 font-mono text-xs"
        >
          {(node as InlineCode).value}
        </code>
      )
    case "break":
      return <br key={key} />
    case "link": {
      const link = node as Link
      const citation = matchCitation(ctx.citationByUrl, link.url)
      // A link that is one of the answer's citations becomes its numbered chip.
      // Left as a link it would print a naked tracking URL mid-sentence, which
      // is what made the raw text unreadable.
      if (citation) return <CitationChip key={key} citation={citation} ctx={ctx} />
      return (
        <a
          key={key}
          href={link.url}
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-2 hover:text-foreground"
        >
          {renderInline(link.children, ctx)}
        </a>
      )
    }
    default:
      return "children" in node ? (
        <span key={key}>
          {renderInline(node.children as PhrasingContent[], ctx)}
        </span>
      ) : null
  }
}

// renderText decorates one text node with whatever mention and citation ranges
// overlap its own [start, end) window in the source. `trim` drops characters
// from the node's ends only, so the remaining text keeps its true source
// offsets and the ranges still line up.
function renderText(
  node: Text,
  key: number,
  ctx: RenderContext,
  trim?: Trim
): ReactNode {
  const head = trim === undefined || trim === "drop" ? 0 : trim.head
  const tail = trim === undefined || trim === "drop" ? 0 : trim.tail
  const rawStart = node.position?.start.offset
  const rawEnd = node.position?.end.offset
  // mdast values are decoded (entities and escapes resolved), so a node whose
  // value length disagrees with its source span cannot be sliced by offset.
  // Render it undecorated rather than highlighting the wrong characters.
  if (
    rawStart === undefined ||
    rawEnd === undefined ||
    rawEnd - rawStart !== node.value.length
  ) {
    return <span key={key}>{node.value.slice(head, node.value.length - tail)}</span>
  }
  const start = rawStart + head
  const end = rawEnd - tail
  if (end <= start) return null

  const boundaries = new Set<number>([start, end])
  const overlapping = <T extends { start: number; end: number }>(ranges: T[]) =>
    ranges.filter((range) => range.start < end && range.end > start)
  const mentions = overlapping(ctx.mentions)
  const citations = overlapping(ctx.citations)
  for (const range of [...mentions, ...citations]) {
    if (range.start > start && range.start < end) boundaries.add(range.start)
    if (range.end > start && range.end < end) boundaries.add(range.end)
  }
  for (const offset of ctx.markersByEnd.keys()) {
    if (offset > start && offset < end) boundaries.add(offset)
  }

  const ordered = [...boundaries].sort((a, b) => a - b)
  const parts: ReactNode[] = []
  for (let i = 0; i < ordered.length - 1; i++) {
    const from = ordered[i]
    const to = ordered[i + 1]
    // Indices are source offsets; node.value starts at rawStart, not at the
    // trimmed start.
    const slice = node.value.slice(from - rawStart, to - rawStart)
    const hit = mentions.filter((range) => from >= range.start && from < range.end)
    const cited = citations.some(
      (range) => from >= range.start && from < range.end
    )
    parts.push(
      <TextSlice
        key={from}
        text={slice}
        mentions={hit}
        cited={cited}
        active={ctx.active}
      />
    )
    for (const citation of ctx.markersByEnd.get(to) ?? []) {
      parts.push(
        <CitationChip key={`m${citation.citeOrder}`} citation={citation} ctx={ctx} />
      )
    }
  }
  return <span key={key}>{parts}</span>
}

function TextSlice({
  text,
  mentions,
  cited,
  active,
}: {
  text: string
  mentions: MentionRange[]
  cited: boolean
  active: AnswerHighlight | undefined
}) {
  if (text.length === 0) return null
  if (mentions.length === 0) {
    return cited ? (
      <span className="border-b border-dotted border-primary/60">{text}</span>
    ) : (
      <>{text}</>
    )
  }
  const flashing =
    active?.kind === "mention" &&
    mentions.some((range) => range.index === active.index)
  return (
    <mark
      id={`mention-${mentions[0].index}`}
      className={cn(
        "scroll-mt-24 rounded-sm px-0.5",
        mentionHighlightClass(mentions.map((range) => range.mention)),
        flashing && "ring-2 ring-primary ring-offset-1 ring-offset-background"
      )}
      title={mentionTitle(mentions.map((range) => range.mention))}
    >
      {text}
    </mark>
  )
}

function CitationChip({
  citation,
  ctx,
}: {
  citation: ResultCitation
  ctx: RenderContext
}) {
  const flashing =
    ctx.active?.kind === "citation" && ctx.active.citeOrder === citation.citeOrder
  return (
    <sup className="ms-0.5 align-super text-[0.65rem] leading-none">
      <a
        id={`citation-ref-${citation.citeOrder}`}
        href={`#citation-${citation.citeOrder}`}
        className={cn(
          "scroll-mt-24 rounded-sm bg-secondary px-1 py-0.5 font-medium text-secondary-foreground no-underline ring-1 ring-border hover:bg-muted",
          flashing && "bg-primary text-primary-foreground ring-primary"
        )}
        title={citation.title ?? citation.domain}
      >
        {citation.citeOrder + 1}
      </a>
    </sup>
  )
}

function visitLinks(nodes: unknown[], visit: (node: Link) => void): void {
  for (const node of nodes) {
    if (typeof node !== "object" || node === null) continue
    const typed = node as { type?: string; children?: unknown[] }
    if (typed.type === "link") visit(node as Link)
    if (Array.isArray(typed.children)) visitLinks(typed.children, visit)
  }
}

// matchCitation tolerates the tracking params OpenAI appends to cited URLs
// differing between the annotation and the inline link.
function matchCitation(
  byUrl: Map<string, ResultCitation>,
  url: string
): ResultCitation | undefined {
  return byUrl.get(url) ?? byUrl.get(withoutQuery(url))
}

function mentionHighlightClass(mentions: ResultMention[]): string {
  const subjects = new Set(mentions.map((mention) => mention.subject))
  if (subjects.size > 1) {
    return "bg-sky-100 text-sky-950 ring-1 ring-sky-200 dark:bg-sky-500/20 dark:text-sky-50 dark:ring-sky-500/30"
  }
  if (subjects.has(MentionSubject.SELF)) {
    return "bg-emerald-100 text-emerald-950 ring-1 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-500/30"
  }
  return "bg-amber-100 text-amber-950 ring-1 ring-amber-200 dark:bg-amber-500/20 dark:text-amber-50 dark:ring-amber-500/30"
}

function mentionTitle(mentions: ResultMention[]): string {
  const labels = [
    ...new Set(mentions.map((mention) => mentionSubjectLabel(mention.subject))),
  ]
  return `${labels.join(" and ")} mention${labels.length === 1 ? "" : "s"}`
}
