package coverage

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return ts
}

// parseT parses an RFC3339 time and panics on error; used where no *testing.T
// is in scope (fixture construction).
func parseT(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func findByARN(fs []Finding, arn string) *Finding {
	for i := range fs {
		if fs[i].Resource.ARN == arn {
			return &fs[i]
		}
	}
	return nil
}

func baseInventory() Inventory {
	return Inventory{
		Account: "111122223333", Region: "eu-central-1",
		Resources: []Resource{
			{ARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-uncov", Type: EBS, ID: "vol-uncov", Tags: map[string]string{"env": "dev"}},
			{ARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-tagcov", Type: EBS, ID: "vol-tagcov", Tags: map[string]string{"backup": "yes"}},
			{ARN: "arn:aws:rds:eu-central-1:111122223333:db:prod-db", Type: RDS, ID: "prod-db", Tags: map[string]string{"env": "prod"}},
			{ARN: "arn:aws:dynamodb:eu-central-1:111122223333:table/orders", Type: DynamoDB, ID: "orders"},
		},
		Selections: []Selection{
			{Plan: "daily", TagConditions: []TagCondition{{Key: "backup", Value: "yes"}}},
			{Plan: "prod-plan", Resources: []string{"arn:aws:rds:eu-central-1:111122223333:db:*"}},
			{Plan: "ddb-plan", Resources: []string{"arn:aws:dynamodb:*:*:table/orders"}},
		},
		RecoveryPoints: []RecoveryPoint{
			{ResourceARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-tagcov", CompletedAt: parseT("2026-09-03T06:00:00Z")},
			// prod-db has an old recovery point -> stale
			{ResourceARN: "arn:aws:rds:eu-central-1:111122223333:db:prod-db", CompletedAt: parseT("2026-08-01T00:00:00Z")},
			// orders (ddb) covered but no recovery point -> stale
		},
	}
}

func TestClassify(t *testing.T) {
	inv := baseInventory()
	now := mustTime(t, "2026-09-03T12:00:00Z")
	findings, sum := Classify(inv, Options{Now: now, StaleAfter: 7 * 24 * time.Hour})

	if sum.Total != 4 {
		t.Fatalf("total = %d, want 4", sum.Total)
	}

	uncov := findByARN(findings, "arn:aws:ec2:eu-central-1:111122223333:volume/vol-uncov")
	if uncov == nil || uncov.Status != StatusUncovered {
		t.Fatalf("vol-uncov = %+v, want uncovered", uncov)
	}

	cov := findByARN(findings, "arn:aws:ec2:eu-central-1:111122223333:volume/vol-tagcov")
	if cov == nil || cov.Status != StatusCovered {
		t.Fatalf("vol-tagcov = %+v, want covered", cov)
	}
	if len(cov.Plans) != 1 || cov.Plans[0] != "daily" {
		t.Fatalf("vol-tagcov plans = %v, want [daily]", cov.Plans)
	}

	stale := findByARN(findings, "arn:aws:rds:eu-central-1:111122223333:db:prod-db")
	if stale == nil || stale.Status != StatusStale {
		t.Fatalf("prod-db = %+v, want stale (old recovery point)", stale)
	}

	noRP := findByARN(findings, "arn:aws:dynamodb:eu-central-1:111122223333:table/orders")
	if noRP == nil || noRP.Status != StatusStale {
		t.Fatalf("orders = %+v, want stale (covered, no recovery point)", noRP)
	}

	if sum.Uncovered != 1 || sum.Stale != 2 || sum.Covered != 1 {
		t.Fatalf("summary = %+v, want 1 uncovered, 2 stale, 1 covered", sum)
	}

	// Worst-first ordering: first finding must be uncovered.
	if findings[0].Status != StatusUncovered {
		t.Fatalf("findings[0] = %s, want uncovered first", findings[0].Status)
	}
}

func TestClassifyTypeFilter(t *testing.T) {
	inv := baseInventory()
	findings, sum := Classify(inv, Options{
		Now:        mustTime(t, "2026-09-03T12:00:00Z"),
		StaleAfter: 7 * 24 * time.Hour,
		Types:      []ResourceType{EBS},
	})
	if sum.Total != 2 {
		t.Fatalf("filtered total = %d, want 2 EBS", sum.Total)
	}
	for _, f := range findings {
		if f.Resource.Type != EBS {
			t.Fatalf("filter leaked %s", f.Resource.Type)
		}
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, arn string
		want         bool
	}{
		{"*", "arn:aws:ec2:x:y:volume/vol-1", true},
		{"arn:aws:rds:eu-central-1:111122223333:db:*", "arn:aws:rds:eu-central-1:111122223333:db:prod-db", true},
		{"arn:aws:rds:*:*:db:*", "arn:aws:rds:eu-central-1:111122223333:db:prod-db", true},
		{"arn:aws:ec2:*:*:volume/*", "arn:aws:rds:eu-central-1:111122223333:db:prod-db", false},
		{"arn:aws:dynamodb:*:*:table/orders", "arn:aws:dynamodb:eu-central-1:111122223333:table/orders", true},
		{"arn:aws:dynamodb:*:*:table/orders", "arn:aws:dynamodb:eu-central-1:111122223333:table/orders-2", false},
		{"prefix", "prefix-and-more", false},
	}
	for _, c := range cases {
		if got := wildcardMatch(c.pattern, c.arn); got != c.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", c.pattern, c.arn, got, c.want)
		}
	}
}

func TestNotResourcesExcludes(t *testing.T) {
	r := Resource{ARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-x", Type: EBS}
	sel := Selection{
		Plan:         "daily",
		Resources:    []string{"arn:aws:ec2:*:*:volume/*"},
		NotResources: []string{"arn:aws:ec2:*:*:volume/vol-x"},
	}
	if sel.covers(r) {
		t.Fatal("NotResources should exclude vol-x")
	}
}

func TestShouldFail(t *testing.T) {
	findings := []Finding{
		{Status: StatusCovered}, {Status: StatusStale},
	}
	uncov := append(findings, Finding{Status: StatusUncovered})

	cases := []struct {
		name   string
		fs     []Finding
		failOn string
		want   bool
	}{
		{"none never fails", uncov, "none", false},
		{"stale trips on stale", findings, "stale", true},
		{"uncovered ignores stale-only", findings, "uncovered", false},
		{"uncovered trips on uncovered", uncov, "uncovered", true},
		{"empty is none", findings, "", false},
	}
	for _, c := range cases {
		got, err := ShouldFail(c.fs, c.failOn)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: ShouldFail=%v, want %v", c.name, got, c.want)
		}
	}

	if _, err := ShouldFail(findings, "bogus"); err == nil {
		t.Error("expected error for invalid --fail-on")
	}
}
