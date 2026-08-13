// Package llm defines model execution boundaries for prompt monitoring and
// analysis (01-D1, D6). OpenAI-backed implementations live behind interfaces so
// future platforms can be added without changing River job orchestration.
//
// Every New*Runner constructor takes the same mode: "stub" and "replay" spend
// no OpenAI money, "openai" is the real call. Only NewPromptRunner has a
// recorded corpus (replay.go) — the extraction, match, and propose-profile
// runners alias "replay" to their stub.
package llm
