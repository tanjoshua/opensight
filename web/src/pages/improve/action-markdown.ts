import type { ImprovementAction } from "@/gen/opensight/v1/improve_pb"
import { contentCategory, substantiationNote } from "@/pages/improve/shared"

// blockquote renders a passage the way an agent will read it back: quoted, so
// the evidence stays distinguishable from the instruction around it.
function blockquote(text: string) {
  return text
    .split("\n")
    .map((line) => (line ? `> ${line}` : ">"))
    .join("\n")
}

// A step may end with a preformatted block after a blank line — the
// structured-data action puts the JSON-LD to paste there. It stays a fenced
// block so the snippet arrives as code rather than as a paragraph whose
// indentation and newlines have been flattened.
function stepMarkdown(step: string) {
  const split = step.indexOf("\n\n")
  if (split === -1) return step
  return `${step.slice(0, split)}\n\n\`\`\`\n${step.slice(split + 2)}\n\`\`\``
}

// indent keeps a step's continuation lines inside its list item; blank lines
// stay blank so the fenced block does not turn into an indented one.
function indent(text: string, prefix: string) {
  return text
    .split("\n")
    .map((line, index) => (index === 0 || !line ? line : prefix + line))
    .join("\n")
}

// actionMarkdown is the card as text: the same finding, evidence, steps and
// sources the user is reading, in a shape they can paste straight into a coding
// agent. It adds no advice of its own — anything not on the card is not here.
export function actionMarkdown(action: ImprovementAction): string {
  const blocks: string[] = [`# ${action.title}`]

  const meta = [
    action.blocking ? "Blocking" : "",
    action.categoryLabel,
    action.reach > 0
      ? `Seen in ${action.reach} monitored answer${action.reach === 1 ? "" : "s"}`
      : "",
  ].filter(Boolean)
  if (meta.length > 0) blocks.push(meta.join(" · "))
  if (action.body) blocks.push(action.body)

  const comparison = action.comparison
  const cited = comparison?.cited ?? []
  const site = comparison?.site ?? []
  if (comparison && (cited.length > 0 || site.length > 0)) {
    if (cited.length > 0) {
      blocks.push("## What monitored answers said")
      for (const quote of cited) {
        blocks.push(`${blockquote(quote.quote)}\n>\n> — cited ${quote.domain}`)
      }
      if (action.detail) blocks.push(action.detail)
    }
    blocks.push("## What your site says")
    if (site.length > 0) {
      for (const quote of site) blocks.push(blockquote(quote))
      if (comparison.coverage === "partial") {
        blocks.push(
          "Closest wording in the pages checked; it stops short of the detail cited alongside."
        )
      }
    } else {
      blocks.push(
        comparison.coverage === "partial"
          ? "Covered in the pages checked, but without the detail cited alongside"
          : "Not found in the pages checked"
      )
    }
  } else if (!comparison && action.detail) {
    blocks.push("## What we found", action.detail)
  }

  if (action.steps.length > 0) {
    blocks.push(
      action.category === contentCategory
        ? "## Suggested next step"
        : "## Do this"
    )
    if (action.steps.length === 1) {
      blocks.push(stepMarkdown(action.steps[0]))
    } else {
      blocks.push(
        action.steps
          .map(
            (step, index) =>
              `${index + 1}. ${indent(stepMarkdown(step), "   ")}`
          )
          .join("\n\n")
      )
    }
    if (action.category === contentCategory) blocks.push(substantiationNote)
  }

  if (action.sources.length > 0) {
    blocks.push("## Sources checked")
    blocks.push(action.sources.map((source) => `- ${source}`).join("\n"))
  }

  return `${blocks.join("\n\n")}\n`
}
