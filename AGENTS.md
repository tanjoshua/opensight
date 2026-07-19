# AGENTS.md

- Docs contain only the finalized plan. Do not store decision history, change logs, or review records — apply changes in place and delete superseded material.
- Doc map: `docs/prd.md` (product truth) · `docs/design/` (technical design, 01–07) · `docs/stories/` (backlog). Consult the relevant design doc before implementing a story.
- The docs are guides, not gospel: if implementation reveals a more optimal solution, adapt — and update the affected doc so it stays the finalized plan.

## Development process

- A tech lead agent using a strong reasoning model should plan the technical implementation.
- The plan can be handed off to an engineer agent for implementation.
- After the engineer agent is done, the engineering manager agent should review the implementation. The engineering manager agent should not make any changes themselves.
- The engineer agent should take into consideration the points raised in the review. Engineer agents are allowed to push back and not make changes if they do not agree with it.
- At an appropriate checkpoint, the changes should be committed and pushed (merged if development was done in a separate branch).
- Code should be clean, concise. The simplest solution should always be prioritized if it doesn't sacrifice software quality.
- For testing, it is important not to clutter the codebase with unnecessary tests. Each test case needs to justify itself for why it is a useful test case.
