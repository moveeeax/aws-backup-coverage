package scan

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
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
