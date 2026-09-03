package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/moveeeax/aws-backup-coverage/internal/coverage"
)

func sampleReport() coverage.Report {
	rp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return coverage.Report{
		Account: "111122223333", Region: "eu-central-1", StaleAfter: "168h0m0s",
		Findings: []coverage.Finding{
			{Resource: coverage.Resource{ARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-1", Type: coverage.EBS, ID: "vol-1"},
				Status: coverage.StatusUncovered, Reason: "no backup plan selection matches this resource"},
			{Resource: coverage.Resource{ARN: "arn:aws:rds:eu-central-1:111122223333:db:prod", Type: coverage.RDS, ID: "prod"},
				Status: coverage.StatusStale, Reason: "latest recovery point is older than 168h0m0s", Plans: []string{"daily"}, LatestRecoveryPoint: &rp},
			{Resource: coverage.Resource{ARN: "arn:aws:efs:eu-central-1:111122223333:file-system/fs-1", Type: coverage.EFS, ID: "fs-1"},
				Status: coverage.StatusCovered, Reason: "covered by daily", Plans: []string{"daily"}},
		},
		Summary: coverage.Summary{Total: 3, Uncovered: 1, Stale: 1, Covered: 1},
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var back coverage.Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("json is not valid/round-trippable: %v", err)
	}
	if back.Summary.Uncovered != 1 || len(back.Findings) != 3 {
		t.Fatalf("round-trip lost data: %+v", back.Summary)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"uncovered", "vol-1", "3 resources", "1 covered", "1 uncovered"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
}

func TestSARIF(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var log sarifLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("sarif is not valid json: %v", err)
	}
	if log.Version != "2.1.0" {
		t.Errorf("version = %q, want 2.1.0", log.Version)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(log.Runs))
	}
	// Only the uncovered + stale findings become results; covered is dropped.
	if got := len(log.Runs[0].Results); got != 2 {
		t.Fatalf("results = %d, want 2 (covered omitted)", got)
	}
	seen := map[string]string{}
	for _, r := range log.Runs[0].Results {
		seen[r.RuleID] = r.Level
	}
	if seen["backup-uncovered"] != "error" {
		t.Errorf("uncovered level = %q, want error", seen["backup-uncovered"])
	}
	if seen["backup-stale"] != "warning" {
		t.Errorf("stale level = %q, want warning", seen["backup-stale"])
	}
}
