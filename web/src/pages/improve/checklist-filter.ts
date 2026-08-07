import type { ChecklistSection } from "@/gen/opensight/v1/improve_pb"
import { ChecklistStanding } from "@/gen/opensight/v1/improve_pb"

export function filterChecklistSections(
  sections: ChecklistSection[],
  standing?: ChecklistStanding
) {
  if (!standing) return sections
  return sections
    .map((section) => ({
      ...section,
      practices: section.practices
        .map((practice) => ({
          ...practice,
          subjects: practice.subjects.filter(
            (subject) => subject.standing === standing
          ),
        }))
        .filter((practice) => practice.subjects.length > 0),
    }))
    .filter((section) => section.practices.length > 0)
}
