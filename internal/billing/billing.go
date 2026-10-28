package billing

import (
	"errors"
	"sort"
)

const (
	StatusUnpaid  = "unpaid"
	StatusOpen    = "open"
	StatusSettled = "settled"
	maxCents      = 10_000_000 * 100
)

var (
	ErrAmount    = errors.New("amount must be greater than zero")
	ErrNoPeople  = errors.New("add at least one person")
	ErrDuplicate = errors.New("a person was included twice")
	ErrNoWeights = errors.New("enter a weight for at least one person")
	ErrBadWeight = errors.New("weights must be zero or greater")
	ErrBadShare  = errors.New("share amounts can't be negative")
	ErrSplit     = errors.New("could not split that amount")
)

type Portion struct {
	UserID      string
	Weight      int
	AmountCents int64
}

type ShareState struct {
	UserID      string
	AmountCents int64
	PaidCents   int64
}

type Debt struct {
	FromID string
	ToID   string
	Cents  int64
}

type SumMismatchError struct {
	Got  int64
	Want int64
}

func (e SumMismatchError) Error() string {
	return "shares do not add up to the bill"
}

func SplitEqual(total int64, userIDs []string) ([]Portion, error) {
	if len(userIDs) == 0 {
		return nil, ErrNoPeople
	}
	seen := make(map[string]struct{}, len(userIDs))
	parts := make([]Portion, 0, len(userIDs))
	for _, id := range userIDs {
		if id == "" {
			return nil, ErrNoPeople
		}
		if _, ok := seen[id]; ok {
			return nil, ErrDuplicate
		}
		seen[id] = struct{}{}
		parts = append(parts, Portion{UserID: id, Weight: 1})
	}
	return SplitWeighted(total, parts)
}

func SplitWeighted(total int64, parts []Portion) ([]Portion, error) {
	if total <= 0 || total > maxCents {
		return nil, ErrAmount
	}
	if len(parts) == 0 {
		return nil, ErrNoPeople
	}
	seen := make(map[string]struct{}, len(parts))
	var sumW int64
	for _, p := range parts {
		if p.UserID == "" {
			return nil, ErrNoPeople
		}
		if _, ok := seen[p.UserID]; ok {
			return nil, ErrDuplicate
		}
		seen[p.UserID] = struct{}{}
		if p.Weight < 0 || p.Weight > 10000 {
			return nil, ErrBadWeight
		}
		sumW += int64(p.Weight)
	}
	if sumW == 0 {
		return nil, ErrNoWeights
	}

	type row struct {
		i   int
		rem int64
		id  string
	}
	out := make([]Portion, len(parts))
	rows := make([]row, len(parts))
	var allocated int64
	for i, p := range parts {
		w := int64(p.Weight)
		if w != 0 && total > (1<<62)/w {
			return nil, ErrAmount
		}
		quota := (total * w) / sumW
		rem := (total * w) % sumW
		out[i] = Portion{UserID: p.UserID, Weight: p.Weight, AmountCents: quota}
		allocated += quota
		rows[i] = row{i: i, rem: rem, id: p.UserID}
	}
	left := total - allocated
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].rem != rows[b].rem {
			return rows[a].rem > rows[b].rem
		}
		return rows[a].id < rows[b].id
	})
	for i := 0; i < len(rows) && left > 0; i++ {
		if rows[i].rem == 0 {
			break
		}
		out[rows[i].i].AmountCents++
		left--
	}
	if left != 0 {
		return nil, ErrSplit
	}
	return out, nil
}

func SplitFixed(total int64, parts []Portion) ([]Portion, error) {
	if total <= 0 || total > maxCents {
		return nil, ErrAmount
	}
	if len(parts) == 0 {
		return nil, ErrNoPeople
	}
	seen := make(map[string]struct{}, len(parts))
	var sum int64
	out := make([]Portion, len(parts))
	for i, p := range parts {
		if p.UserID == "" {
			return nil, ErrNoPeople
		}
		if _, ok := seen[p.UserID]; ok {
			return nil, ErrDuplicate
		}
		seen[p.UserID] = struct{}{}
		if p.AmountCents < 0 {
			return nil, ErrBadShare
		}
		sum += p.AmountCents
		out[i] = Portion{UserID: p.UserID, AmountCents: p.AmountCents}
	}
	if sum != total {
		return nil, SumMismatchError{Got: sum, Want: total}
	}
	return out, nil
}

func DeriveStatus(payerID string, shares []ShareState) string {
	if payerID == "" {
		return StatusUnpaid
	}
	for _, sh := range shares {
		if sh.UserID == payerID {
			continue
		}
		if sh.PaidCents < sh.AmountCents {
			return StatusOpen
		}
	}
	return StatusSettled
}

func Debts(payerID string, shares []ShareState) []Debt {
	if payerID == "" {
		return nil
	}
	var out []Debt
	for _, sh := range shares {
		if sh.UserID == payerID {
			continue
		}
		left := sh.AmountCents - sh.PaidCents
		if left > 0 {
			out = append(out, Debt{FromID: sh.UserID, ToID: payerID, Cents: left})
		}
	}
	return out
}

func Net(debts []Debt) []Debt {
	type key struct{ a, b string }
	signed := make(map[key]int64)
	for _, d := range debts {
		if d.Cents <= 0 || d.FromID == "" || d.ToID == "" || d.FromID == d.ToID {
			continue
		}
		if d.FromID < d.ToID {
			signed[key{d.FromID, d.ToID}] += d.Cents
		} else {
			signed[key{d.ToID, d.FromID}] -= d.Cents
		}
	}
	out := make([]Debt, 0, len(signed))
	for k, v := range signed {
		switch {
		case v > 0:
			out = append(out, Debt{FromID: k.a, ToID: k.b, Cents: v})
		case v < 0:
			out = append(out, Debt{FromID: k.b, ToID: k.a, Cents: -v})
		}
	}
	return out
}
