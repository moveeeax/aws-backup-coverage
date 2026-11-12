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

func multiRegionReport() coverage.Report {
	findings := []coverage.Finding{
		{Resource: coverage.Resource{ARN: "arn:aws:ec2:us-east-1:111122223333:volume/vol-us", Type: coverage.EBS, ID: "vol-us", Region: "us-east-1"},
			Status: coverage.StatusUncovered, Reason: "no backup plan selection matches this resource"},
		{Resource: coverage.Resource{ARN: "arn:aws:ec2:eu-central-1:111122223333:volume/vol-eu", Type: coverage.EBS, ID: "vol-eu", Region: "eu-central-1"},
			Status: coverage.StatusCovered, Reason: "covered by eu-daily", Plans: []string{"eu-daily"}},
	}
	return coverage.Report{
		Account: "111122223333", StaleAfter: "168h0m0s", Findings: findings,
		Summary: coverage.Summary{Total: 2, Uncovered: 1, Covered: 1},
		Regions: coverage.SummarizeByRegion(findings),
	}
}

func TestTableMultiRegion(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, multiRegionReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"REGION", "us-east-1", "2 resources",
		"eu-central-1:  1 resources: 1 covered, 0 stale, 0 uncovered",
		"us-east-1:     1 resources: 0 covered, 0 stale, 1 uncovered"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
}

func TestTableSingleRegionNoBreakdown(t *testing.T) {
	rep := sampleReport()
	rep.Regions = []coverage.RegionSummary{{Region: "eu-central-1", Summary: rep.Summary}}
	var buf bytes.Buffer
	if err := Table(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "eu-central-1:") {
		t.Errorf("single-region table should not print a per-region breakdown\n%s", buf.String())
	}
}

func TestJSONCarriesRegion(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, multiRegionReport()); err != nil {
		t.Fatal(err)
	}
	var back coverage.Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Findings[0].Resource.Region != "us-east-1" {
		t.Errorf("finding region = %q", back.Findings[0].Resource.Region)
	}
	if len(back.Regions) != 2 || back.Regions[1].Region != "us-east-1" || back.Regions[1].Uncovered != 1 {
		t.Errorf("regions = %+v", back.Regions)
	}
}

func TestSARIFRegion(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, multiRegionReport()); err != nil {
		t.Fatal(err)
	}
	var log sarifLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	res := log.Runs[0].Results
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	if res[0].Properties["region"] != "us-east-1" {
		t.Errorf("properties = %v, want region us-east-1", res[0].Properties)
	}
	if !strings.Contains(res[0].Message.Text, "us-east-1") {
		t.Errorf("message = %q, want region in text", res[0].Message.Text)
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
