package queueeval

import (
	"context"
	"sync"
)

// ParallelMap runs every item, limits in-process concurrency, and returns
// successful values. Individual failures are reported to onError and skipped,
// matching monitoring's partial-result posture. Context cancellation stops
// scheduling new work and is returned to the caller.
func ParallelMap[T, R any](ctx context.Context, limit int, items []T, run func(context.Context, T) (R, error), onError func(T, error)) ([]R, error) {
	if limit < 1 {
		limit = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, limit)
	results := make([]R, len(items))
	ok := make([]bool, len(items))
	var wg sync.WaitGroup

	for i, item := range items {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := run(ctx, item)
			if err != nil {
				onError(item, err)
				return
			}
			results[i], ok[i] = result, true
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	compacted := make([]R, 0, len(results))
	for i := range results {
		if ok[i] {
			compacted = append(compacted, results[i])
		}
	}
	return compacted, nil
}
