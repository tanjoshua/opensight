// Package workflows holds the Temporal workflows and activities: onboarding,
// scheduled monitoring runs, and analysis (01-D5). RunWorkflow (design 04)
// snapshots a run, fans out one ExecutePrompt activity per prompt, and
// finalizes the run status.
package workflows
