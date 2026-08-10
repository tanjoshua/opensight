// contentCategory mirrors visibility.CategoryContent. Only these actions ask the
// user to write something, so only they carry the substantiation note.
export const contentCategory = "content"

// substantiationNote is a constraint on how every website-content change is
// written, not a second task. It travels with the action wherever the action
// goes — on the card, and in the copy a coding agent is handed.
export const substantiationNote =
  "Publish only facts you can substantiate. Do not copy another business’s wording or imply outcomes you cannot support."
