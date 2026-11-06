package runs

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// price is USD per million tokens.
type price struct{ in, out, cacheRead, cacheWrite float64 }

// prices are matched by model-ID prefix, in order. Cache writes are the
// 5-minute TTL rate (1.25x input).
var prices = []struct {
	prefix string
	price
}{
	{"claude-haiku-5-5", price{0.10, 0.50, 0.01, 0.125}},
	{"claude-sonnet-5-5", price{2, 10, 0.20, 2.50}},
	{"claude-opus-5-5", price{4, 20, 0.40, 5.00}},
}

// dearest prices anything unknown, so a surprise model never under-counts.
var dearest = prices[len(prices)-1].price

func (p price) cost(c Counts) float64 {
	return (float64(c.Input)*p.in + float64(c.Output)*p.out +
		float64(c.CacheRead)*p.cacheRead + float64(c.CacheWrite)*p.cacheWrite) / 1e6
}

// Cost prices a run's usage in USD. A usage without a per-model split, or with
// an unknown model, is priced at the dearest rate.
func Cost(u Usage) float64 {
	if len(u.ByModel) == 0 {
		return dearest.cost(u.Counts)
	}
	var total float64
	for model, c := range u.ByModel {
		p := dearest
		for _, row := range prices {
			if strings.HasPrefix(model, row.prefix) {
				p = row.price
				break
			}
		}
		total += p.cost(c)
	}
	// Top-level counts beyond what the split accounts for are priced dearest,
	// so mismatched reports can never under-count.
	split := splitSum(u.ByModel)
	excess := Counts{Input: over(u.Input, split.Input), Output: over(u.Output, split.Output),
		CacheRead: over(u.CacheRead, split.CacheRead), CacheWrite: over(u.CacheWrite, split.CacheWrite)}
	return total + dearest.cost(excess)
}

func over(total, part uint64) uint64 {
	if total > part {
		return total - part
	}
	return 0
}

// DayStart is the most recent midnight in loc at or before now, as UTC.
func DayStart(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC()
}

// SpentSince sums the cost of every run started at or after t, counting a
// running run's latest reported usage.
func (s *Service) SpentSince(t time.Time) (float64, error) {
	rs, err := s.Store.RunsSince(t)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, r := range rs {
		u, err := s.Usage(r.ID)
		if err != nil {
			return 0, err
		}
		if u != nil {
			total += Cost(*u)
		}
	}
	return total, nil
}

var (
	ErrPaused     = errors.New("the cast is paused; resume it to start runs")
	ErrOverBudget = errors.New("today's budget is spent")
)

// Gate is the single check every run start passes: nil, ErrPaused, or a
// wrapped ErrOverBudget. Paused is checked first: it is cheaper and clearer.
func (s *Service) Gate(now time.Time) error {
	paused, err := s.Store.Paused()
	if err != nil {
		return err
	}
	if paused {
		return ErrPaused
	}
	p, err := s.Store.Project()
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	loc, budget := p.Location(), p.Budget()
	spent, err := s.SpentSince(DayStart(now, loc))
	if err != nil {
		return err
	}
	if spent >= budget {
		return fmt.Errorf("%w: $%.2f of $%.2f; runs start again at midnight (%s) or when you raise the budget", ErrOverBudget, spent, budget, loc)
	}
	return nil
}
