package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/santiagolertora/yb-doctor/internal/domain"
)

// WatchAnalyze collects and analyzes until ctx is done, emitting each sample.
// The returned report is the last sample, with Diff versus the first sample.
func (s *Service) WatchAnalyze(ctx context.Context, interval time.Duration, emit func(*domain.HealthReport) error) (*domain.HealthReport, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("app: watch interval must be > 0")
	}
	first, err := s.Analyze(ctx)
	if err != nil {
		return nil, err
	}
	if emit != nil {
		if err := emit(first); err != nil {
			return nil, fmt.Errorf("emit analyze: %w", err)
		}
	}
	last := first
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			last.Diff = DiffReports("watch start", first, last)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
				return last, nil
			}
			return last, fmt.Errorf("watch: %w", ctx.Err())
		case <-tick.C:
			rep, err := s.Analyze(ctx)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					last.Diff = DiffReports("watch start", first, last)
					return last, nil
				}
				return last, err
			}
			last = rep
			if emit != nil {
				if err := emit(last); err != nil {
					return last, fmt.Errorf("emit analyze: %w", err)
				}
			}
		}
	}
}
