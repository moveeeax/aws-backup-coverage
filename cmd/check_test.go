package cmd

import (
	"strings"
	"testing"
)

func TestNormalizeRegions(t *testing.T) {
	got := normalizeRegions([]string{" eu-central-1", "US-EAST-1", "", "eu-central-1"})
	if strings.Join(got, ",") != "eu-central-1,us-east-1" {
		t.Fatalf("normalizeRegions = %v, want trimmed, lowercased, de-duplicated in order", got)
	}
	if got := normalizeRegions(nil); len(got) != 0 {
		t.Fatalf("normalizeRegions(nil) = %v, want empty", got)
	}
}

func TestRegionFlagsExclusive(t *testing.T) {
	cases := [][]string{
		{"check", "--region", "eu-central-1", "--all-regions"},
		{"check", "--from-file", "x.json", "--region", "eu-central-1"},
		{"check", "--from-file", "x.json", "--all-regions"},
	}
	for _, args := range cases {
		root := newRootCmd()
		root.SetArgs(args)
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
			t.Errorf("%v: err = %v, want mutually exclusive flag error", args, err)
		}
	}
}

func TestRegionFlagRepeatable(t *testing.T) {
	c := newCheckCmd()
	if err := c.ParseFlags([]string{"--region", "eu-central-1", "--region", "us-east-1,us-west-2"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Flags().GetStringSlice("region")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "eu-central-1,us-east-1,us-west-2" {
		t.Fatalf("--region = %v", got)
	}
}
