package api

import (
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
)

func citationSourceToProto(source metrics.CitationSource) *opensightv1.CitationSource {
	resp := &opensightv1.CitationSource{
		Domain:    source.Domain,
		Frequency: int32(source.Frequency),
		Subjects:  citationSubjectsToProto(source.Subjects),
		ResultIds: idStrings(source.ResultIDs),
		Pages:     make([]*opensightv1.CitationPage, 0, len(source.Pages)),
		Prompts:   make([]*opensightv1.CitationPromptStat, 0, len(source.Prompts)),
	}
	for _, page := range source.Pages {
		resp.Pages = append(resp.Pages, &opensightv1.CitationPage{
			Url:       page.URL,
			Title:     page.Title,
			Frequency: int32(page.Frequency),
			Subjects:  citationSubjectsToProto(page.Subjects),
			ResultIds: idStrings(page.ResultIDs),
		})
	}
	for _, prompt := range source.Prompts {
		resp.Prompts = append(resp.Prompts, &opensightv1.CitationPromptStat{
			PromptId:   prompt.PromptID.String(),
			PromptText: prompt.Text,
			Frequency:  int32(prompt.Frequency),
			ResultIds:  idStrings(prompt.ResultIDs),
		})
	}
	return resp
}

// citationSubjectsToProto shapes a subject breakdown. All subject
// buckets and the returned message are always non-nil, even when zero-valued.
func citationSubjectsToProto(subjects metrics.CitationSubjectBreakdown) *opensightv1.CitationSubjects {
	return &opensightv1.CitationSubjects{
		Business:   citationSubjectToProto(subjects.Business),
		Competitor: citationSubjectToProto(subjects.Competitor),
		Other:      citationSubjectToProto(subjects.Other),
		Unknown:    citationSubjectToProto(subjects.Unknown),
	}
}

func citationSubjectToProto(subject metrics.CitationSubjectStat) *opensightv1.CitationSubjectStat {
	return &opensightv1.CitationSubjectStat{
		Frequency: int32(subject.Frequency),
		ResultIds: idStrings(subject.ResultIDs),
	}
}
