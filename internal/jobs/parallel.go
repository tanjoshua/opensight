package jobs

import (
	"context"
	"sync"
)

func parallelMap[T, R any](ctx context.Context, limit int, items []T, run func(context.Context, T) (R, error), onError func(T, error)) ([]R, error) {
	if limit < 1 {
		limit = 1
	}
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
		go func(i int, item T) {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := run(ctx, item)
			if err != nil {
				onError(item, err)
				return
			}
			results[i], ok[i] = out, true
		}(i, item)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]R, 0, len(items))
	for i := range results {
		if ok[i] {
			out = append(out, results[i])
		}
	}
	return out, nil
}
