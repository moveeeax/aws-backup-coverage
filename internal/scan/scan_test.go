package scan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	bkptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/moveeeax/aws-backup-coverage/internal/coverage"
)

// fakeEC2 returns two pages of volumes to exercise NextToken paging.
type fakeEC2 struct{ calls int }

func (f *fakeEC2) DescribeVolumes(_ context.Context, in *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	f.calls++
	if in.NextToken == nil {
		return &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId: aws.String("vol-1"),
				Tags:     []ec2types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
			}},
			NextToken: aws.String("page2"),
		}, nil
	}
	return &ec2.DescribeVolumesOutput{
		Volumes: []ec2types.Volume{{VolumeId: aws.String("vol-2")}},
	}, nil
}

func TestCollectEBSPaging(t *testing.T) {
	s := &Scanner{Account: "111122223333", Region: "eu-central-1", EC2: &fakeEC2{}}
	got, err := s.collectEBS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("volumes = %d, want 2 across two pages", len(got))
	}
	if got[0].ARN != "arn:aws:ec2:eu-central-1:111122223333:volume/vol-1" {
		t.Errorf("arn = %q", got[0].ARN)
	}
	if got[0].Type != coverage.EBS || got[0].Tags["env"] != "prod" {
		t.Errorf("resource not mapped: %+v", got[0])
	}
}

func TestToSelection(t *testing.T) {
	sel := &bkptypes.BackupSelection{
		SelectionName: aws.String("by-tag"),
		Resources:     []string{"arn:aws:ec2:*:*:volume/*"},
		NotResources:  []string{"arn:aws:ec2:*:*:volume/vol-skip"},
		ListOfTags: []bkptypes.Condition{{
			ConditionType:  bkptypes.ConditionTypeStringequals,
			ConditionKey:   aws.String("backup"),
			ConditionValue: aws.String("yes"),
		}},
		Conditions: &bkptypes.Conditions{
			StringEquals: []bkptypes.ConditionParameter{{
				ConditionKey:   aws.String("aws:ResourceTag/team"),
				ConditionValue: aws.String("payments"),
			}},
		},
	}
	got := toSelection("daily", sel)
	if got.Plan != "daily" || got.Name != "by-tag" {
		t.Fatalf("plan/name = %q/%q", got.Plan, got.Name)
	}
	if len(got.Resources) != 1 || len(got.NotResources) != 1 {
		t.Fatalf("resources/notResources not carried: %+v", got)
	}
	if len(got.TagConditions) != 2 {
		t.Fatalf("tag conditions = %d, want 2 (ListOfTags + Conditions)", len(got.TagConditions))
	}
	// The aws:ResourceTag/ prefix must be stripped so it matches a plain tag key.
	var haveTeam bool
	for _, c := range got.TagConditions {
		if c.Key == "team" && c.Value == "payments" {
			haveTeam = true
		}
	}
	if !haveTeam {
		t.Errorf("expected stripped tag key 'team': %+v", got.TagConditions)
	}
}

func TestToSelectionNil(t *testing.T) {
	got := toSelection("p", nil)
	if got.Plan != "p" || len(got.TagConditions) != 0 {
		t.Fatalf("nil selection mishandled: %+v", got)
	}
}

type fakeRegions struct{ names []string }

func (f fakeRegions) DescribeRegions(_ context.Context, in *ec2.DescribeRegionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	if in.AllRegions != nil && *in.AllRegions {
		return nil, errors.New("AllRegions must not be set: disabled regions cannot be scanned")
	}
	out := &ec2.DescribeRegionsOutput{}
	for _, n := range f.names {
		out.Regions = append(out.Regions, ec2types.Region{RegionName: aws.String(n)})
	}
	return out, nil
}

func TestListRegionsSorted(t *testing.T) {
	got, err := listRegions(context.Background(), fakeRegions{names: []string{"us-east-1", "eu-central-1", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "eu-central-1,us-east-1" {
		t.Fatalf("regions = %v, want sorted, empty names dropped", got)
	}
}

// fakeVolumes returns one untagged volume per region.
type fakeVolumes struct{ id string }

func (f fakeVolumes) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{{VolumeId: aws.String(f.id)}}}, nil
}

// fakeBackup has one plan with one selection matching every EBS volume (any
// region in the ARN pattern) and no recovery points.
type fakeBackup struct{ plan string }

func (f fakeBackup) ListBackupPlans(context.Context, *backup.ListBackupPlansInput, ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error) {
	return &backup.ListBackupPlansOutput{BackupPlansList: []bkptypes.BackupPlansListMember{{
		BackupPlanId: aws.String("p1"), BackupPlanName: aws.String(f.plan),
	}}}, nil
}

func (f fakeBackup) ListBackupSelections(context.Context, *backup.ListBackupSelectionsInput, ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error) {
	return &backup.ListBackupSelectionsOutput{BackupSelectionsList: []bkptypes.BackupSelectionsListMember{{
		SelectionId: aws.String("s1"),
	}}}, nil
}

func (f fakeBackup) GetBackupSelection(context.Context, *backup.GetBackupSelectionInput, ...func(*backup.Options)) (*backup.GetBackupSelectionOutput, error) {
	return &backup.GetBackupSelectionOutput{BackupSelection: &bkptypes.BackupSelection{
		SelectionName: aws.String("all-ebs"),
		Resources:     []string{"arn:aws:ec2:*:*:volume/*"},
	}}, nil
}

func (f fakeBackup) ListRecoveryPointsByResource(context.Context, *backup.ListRecoveryPointsByResourceInput, ...func(*backup.Options)) (*backup.ListRecoveryPointsByResourceOutput, error) {
	return &backup.ListRecoveryPointsByResourceOutput{}, nil
}

func TestInventoryRegionsMerges(t *testing.T) {
	scanners := map[string]*Scanner{
		"eu-central-1": {Account: "111122223333", Region: "eu-central-1", EC2: fakeVolumes{id: "vol-eu"}, Backup: fakeBackup{plan: "eu-daily"}},
		"us-east-1":    {Account: "111122223333", Region: "us-east-1", EC2: fakeVolumes{id: "vol-us"}, Backup: fakeBackup{plan: "us-daily"}},
	}
	inv, err := InventoryRegions(context.Background(), func(r string) *Scanner { return scanners[r] },
		[]string{"eu-central-1", "us-east-1"}, []coverage.ResourceType{coverage.EBS})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Account != "111122223333" || inv.Region != "" || len(inv.Regions) != 2 {
		t.Fatalf("merged metadata = account %q region %q regions %v", inv.Account, inv.Region, inv.Regions)
	}
	if len(inv.Resources) != 2 || len(inv.Selections) != 2 {
		t.Fatalf("resources/selections = %d/%d, want 2/2", len(inv.Resources), len(inv.Selections))
	}
	for _, r := range inv.Resources {
		if !strings.Contains(r.ARN, ":"+r.Region+":") {
			t.Errorf("resource %s stamped with region %q", r.ARN, r.Region)
		}
	}
	wantRegion := map[string]string{"eu-daily": "eu-central-1", "us-daily": "us-east-1"}
	for _, s := range inv.Selections {
		if s.Region != wantRegion[s.Plan] {
			t.Errorf("selection of plan %s stamped with region %q, want %q", s.Plan, s.Region, wantRegion[s.Plan])
		}
	}
}

func TestInventoryRegionsSingleKeepsRegion(t *testing.T) {
	sc := &Scanner{Account: "111122223333", Region: "eu-west-1", EC2: fakeVolumes{id: "vol-1"}, Backup: fakeBackup{plan: "daily"}}
	inv, err := InventoryRegions(context.Background(), func(string) *Scanner { return sc },
		[]string{""}, []coverage.ResourceType{coverage.EBS})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Region != "eu-west-1" || len(inv.Regions) != 1 {
		t.Fatalf("single-region inventory: region %q regions %v", inv.Region, inv.Regions)
	}
}

type failingVolumes struct{}

func (failingVolumes) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return nil, errors.New("boom")
}

func TestInventoryRegionsErrorNamesRegion(t *testing.T) {
	sc := &Scanner{Region: "ap-south-1", EC2: failingVolumes{}}
	_, err := InventoryRegions(context.Background(), func(string) *Scanner { return sc },
		[]string{"ap-south-1"}, []coverage.ResourceType{coverage.EBS})
	if err == nil || !strings.Contains(err.Error(), "region ap-south-1") {
		t.Fatalf("err = %v, want it to name the region", err)
	}
}
