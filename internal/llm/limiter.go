package llm

import "context"

// Limiter is the process-wide admission gate shared by every LLM call,
// including synchronous RPCs and background jobs.
type Limiter struct{ tokens chan struct{} }

func NewLimiter(concurrency int) *Limiter {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Limiter{tokens: make(chan struct{}, concurrency)}
}

func (l *Limiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case l.tokens <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) Release() {
	if l != nil {
		<-l.tokens
	}
}
