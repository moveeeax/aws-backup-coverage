package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moveeeax/aws-backup-coverage/internal/coverage"
	"github.com/moveeeax/aws-backup-coverage/internal/report"
	"github.com/moveeeax/aws-backup-coverage/internal/scan"
)

func newCheckCmd() *cobra.Command {
	var (
		fromFile   string
		typesCSV   string
		output     string
		staleAfter string
		failOn     string
		regions    []string
		allRegions bool
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check backup coverage for the account and one or more regions",
		RunE: func(cmd *cobra.Command, args []string) error {
			types, err := parseTypes(typesCSV)
			if err != nil {
				return err
			}
			stale, err := parseStaleAfter(staleAfter)
			if err != nil {
				return err
			}

			var inv coverage.Inventory
			if fromFile != "" {
				inv, err = loadInventory(fromFile)
			} else {
				inv, err = liveInventory(cmd.Context(), types, regions, allRegions)
			}
			if err != nil {
				return err
			}

			findings, sum := coverage.Classify(inv, coverage.Options{
				Now:        time.Now().UTC(),
				StaleAfter: stale,
				Types:      types,
			})
			rep := coverage.Report{
				Account: inv.Account, Region: inv.Region,
				Findings: findings, Summary: sum, StaleAfter: stale.String(),
				Regions: coverage.SummarizeByRegion(findings),
			}

			if err := write(os.Stdout, output, rep); err != nil {
				return err
			}

			fail, err := coverage.ShouldFail(findings, failOn)
			if err != nil {
				return err
			}
			if fail {
				return gateError{msg: fmt.Sprintf("--fail-on %s: %d uncovered, %d stale", failOn, sum.Uncovered, sum.Stale)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFile, "from-file", "", "read an inventory JSON instead of calling AWS (offline/CI)")
	cmd.Flags().StringVar(&typesCSV, "types", "", "comma-separated resource types to check (ebs,rds,dynamodb,efs); default all")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json|sarif")
	cmd.Flags().StringVar(&staleAfter, "stale-after", "7d", "age after which a covered resource's latest recovery point is stale (e.g. 24h, 7d)")
	cmd.Flags().StringVar(&failOn, "fail-on", "none", "exit non-zero when findings reach this level: none|stale|uncovered")
	cmd.Flags().StringSliceVar(&regions, "region", nil, "region to scan; repeat or comma-separate for several (default: region from the AWS config chain)")
	cmd.Flags().BoolVar(&allRegions, "all-regions", false, "scan every region enabled for the account (ec2:DescribeRegions)")
	cmd.MarkFlagsMutuallyExclusive("region", "all-regions")
	cmd.MarkFlagsMutuallyExclusive("from-file", "region")
	cmd.MarkFlagsMutuallyExclusive("from-file", "all-regions")
	return cmd
}

func parseTypes(csv string) ([]coverage.ResourceType, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}
	valid := map[string]coverage.ResourceType{
		"ebs": coverage.EBS, "rds": coverage.RDS,
		"dynamodb": coverage.DynamoDB, "ddb": coverage.DynamoDB, "efs": coverage.EFS,
	}
	var out []coverage.ResourceType
	for _, part := range strings.Split(csv, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		t, ok := valid[p]
		if !ok {
			return nil, fmt.Errorf("unknown resource type %q (want ebs,rds,dynamodb,efs)", part)
		}
		out = append(out, t)
	}
	return out, nil
}

// parseStaleAfter accepts Go durations plus a day suffix, e.g. "7d" or "36h".
func parseStaleAfter(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 7 * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid --stale-after %q: %w", s, err)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --stale-after %q: %w", s, err)
	}
	return d, nil
}

func loadInventory(path string) (coverage.Inventory, error) {
	var inv coverage.Inventory
	b, err := os.ReadFile(path)
	if err != nil {
		return inv, err
	}
	if err := json.Unmarshal(b, &inv); err != nil {
		return inv, fmt.Errorf("parse inventory %s: %w", path, err)
	}
	return inv, nil
}

func liveInventory(ctx context.Context, types []coverage.ResourceType, regions []string, allRegions bool) (coverage.Inventory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sess, err := scan.NewSession(ctx)
	if err != nil {
		return coverage.Inventory{}, err
	}
	if allRegions {
		regions, err = sess.EnabledRegions(ctx)
		if err != nil {
			return coverage.Inventory{}, err
		}
	} else {
		regions = normalizeRegions(regions)
	}
	if len(regions) == 0 {
		// No flag: one scan in the region from the AWS config chain.
		regions = []string{""}
	}
	return scan.InventoryRegions(ctx, sess.Scanner, regions, types)
}

// normalizeRegions trims, lowercases and de-duplicates --region values,
// keeping the order they were given in.
func normalizeRegions(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range in {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

func write(w *os.File, format string, rep coverage.Report) error {
	switch strings.ToLower(format) {
	case "", "table":
		return report.Table(w, rep)
	case "json":
		return report.JSON(w, rep)
	case "sarif":
		return report.SARIF(w, rep)
	default:
		return fmt.Errorf("unknown --output %q (want table|json|sarif)", format)
	}
}
