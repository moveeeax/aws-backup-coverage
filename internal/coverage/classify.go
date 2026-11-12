package coverage

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Options controls classification. Now is injected so tests are deterministic.
type Options struct {
	Now        time.Time
	StaleAfter time.Duration
	Types      []ResourceType // if non-empty, only these types are classified
}

// wildcardMatch reports whether arn matches an AWS-style pattern where '*'
// matches any run of characters (including ':' and '/'). A bare "*" matches
// everything. Matching is case-sensitive, like ARNs.
func wildcardMatch(pattern, arn string) bool {
	if pattern == "*" || pattern == arn {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(arn[pos:], part)
		if idx < 0 {
			return false
		}
		// The first segment must anchor at the start of the ARN.
		if i == 0 && idx != 0 {
			return false
		}
		pos += idx + len(part)
	}
	// A pattern that does not end in '*' must consume the ARN to the end.
	if !strings.HasSuffix(pattern, "*") && pos != len(arn) {
		return false
	}
	return true
}

// covers reports whether the selection assigns the resource to its plan. Both
// regions must already be resolved against the inventory default.
func (s Selection) covers(r Resource) bool {
	if s.Region != "" && r.Region != "" && s.Region != r.Region {
		return false
	}
	for _, p := range s.NotResources {
		if wildcardMatch(p, r.ARN) {
			return false
		}
	}
	if len(s.Resources) == 0 && len(s.TagConditions) == 0 {
		return false
	}
	byARN := false
	for _, p := range s.Resources {
		if wildcardMatch(p, r.ARN) {
			byARN = true
			break
		}
	}
	byTag := len(s.TagConditions) > 0
	for _, c := range s.TagConditions {
		if r.Tags[c.Key] != c.Value {
			byTag = false
			break
		}
	}
	return byARN || byTag
}

// Classify evaluates every resource in the inventory and returns findings plus
// a summary. Missing or overdue recovery points on an otherwise-covered
// resource are reported as stale.
func Classify(inv Inventory, opts Options) ([]Finding, Summary) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Latest recovery point per resource ARN.
	latest := map[string]time.Time{}
	for _, rp := range inv.RecoveryPoints {
		if cur, ok := latest[rp.ResourceARN]; !ok || rp.CompletedAt.After(cur) {
			latest[rp.ResourceARN] = rp.CompletedAt
		}
	}

	typeFilter := map[ResourceType]bool{}
	for _, t := range opts.Types {
		typeFilter[t] = true
	}

	sels := make([]Selection, len(inv.Selections))
	for i, sel := range inv.Selections {
		if sel.Region == "" {
			sel.Region = inv.Region
		}
		sels[i] = sel
	}

	var findings []Finding
	for _, r := range inv.Resources {
		if len(typeFilter) > 0 && !typeFilter[r.Type] {
			continue
		}
		if r.Region == "" {
			r.Region = inv.Region
		}

		var plans []string
		for _, sel := range sels {
			if sel.covers(r) {
				plans = append(plans, sel.Plan)
			}
		}
		plans = dedupeSorted(plans)

		f := Finding{Resource: r, Plans: plans}
		if len(plans) == 0 {
			f.Status = StatusUncovered
			f.Reason = "no backup plan selection matches this resource"
			findings = append(findings, f)
			continue
		}

		rp, has := latest[r.ARN]
		if has {
			f.LatestRecoveryPoint = &rp
		}
		switch {
		case !has:
			f.Status = StatusStale
			f.Reason = fmt.Sprintf("covered by %s but has no recovery point", strings.Join(plans, ", "))
		case now.Sub(rp) > opts.StaleAfter:
			f.Status = StatusStale
			f.Reason = fmt.Sprintf("latest recovery point %s is older than %s", rp.UTC().Format(time.RFC3339), opts.StaleAfter)
		default:
			f.Status = StatusCovered
			f.Reason = fmt.Sprintf("covered by %s", strings.Join(plans, ", "))
		}
		findings = append(findings, f)
	}

	// Stable output: worst status first, then by ARN.
	sort.SliceStable(findings, func(i, j int) bool {
		si, sj := findings[i].Status.severity(), findings[j].Status.severity()
		if si != sj {
			return si > sj
		}
		return findings[i].Resource.ARN < findings[j].Resource.ARN
	})

	sum := Summary{Total: len(findings)}
	for _, f := range findings {
		switch f.Status {
		case StatusCovered:
			sum.Covered++
		case StatusUncovered:
			sum.Uncovered++
		case StatusStale:
			sum.Stale++
		}
	}
	return findings, sum
}

// SummarizeByRegion counts findings per resource region, sorted by region
// name. Findings without a region are grouped under an empty region name.
func SummarizeByRegion(findings []Finding) []RegionSummary {
	idx := map[string]int{}
	var out []RegionSummary
	for _, f := range findings {
		i, ok := idx[f.Resource.Region]
		if !ok {
			i = len(out)
			idx[f.Resource.Region] = i
			out = append(out, RegionSummary{Region: f.Resource.Region})
		}
		s := &out[i].Summary
		s.Total++
		switch f.Status {
		case StatusCovered:
			s.Covered++
		case StatusUncovered:
			s.Uncovered++
		case StatusStale:
			s.Stale++
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out
}

// ShouldFail reports whether any finding is at or worse than the failOn
// threshold. failOn is one of "none", "stale", "uncovered".
func ShouldFail(findings []Finding, failOn string) (bool, error) {
	var threshold int
	switch strings.ToLower(strings.TrimSpace(failOn)) {
	case "", "none":
		return false, nil
	case "stale":
		threshold = StatusStale.severity()
	case "uncovered":
		threshold = StatusUncovered.severity()
	default:
		return false, fmt.Errorf("invalid --fail-on %q (want none|stale|uncovered)", failOn)
	}
	for _, f := range findings {
		if f.Status.severity() >= threshold {
			return true, nil
		}
	}
	return false, nil
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
