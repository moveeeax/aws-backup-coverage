// Package scan populates a coverage.Inventory from a live AWS account using the
// AWS SDK for Go v2. Every call is read-only (Describe/List/Get). Service access
// is expressed through narrow single-method interfaces so the collectors and the
// backup-selection transform can be unit-tested with fakes.
package scan

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	bkptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/moveeeax/aws-backup-coverage/internal/coverage"
)

// Minimal read-only client interfaces (only the methods we call).
type ec2API interface {
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}
type rdsAPI interface {
	DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
}
type ddbAPI interface {
	ListTables(context.Context, *dynamodb.ListTablesInput, ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
}
type efsAPI interface {
	DescribeFileSystems(context.Context, *efs.DescribeFileSystemsInput, ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error)
}
type backupAPI interface {
	ListBackupPlans(context.Context, *backup.ListBackupPlansInput, ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error)
	ListBackupSelections(context.Context, *backup.ListBackupSelectionsInput, ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error)
	GetBackupSelection(context.Context, *backup.GetBackupSelectionInput, ...func(*backup.Options)) (*backup.GetBackupSelectionOutput, error)
	ListRecoveryPointsByResource(context.Context, *backup.ListRecoveryPointsByResourceInput, ...func(*backup.Options)) (*backup.ListRecoveryPointsByResourceOutput, error)
}

// Scanner holds the read-only service clients plus account/region context.
type Scanner struct {
	Account string
	Region  string
	EC2     ec2API
	RDS     rdsAPI
	DDB     ddbAPI
	EFS     efsAPI
	Backup  backupAPI
}

// New builds a Scanner from the default AWS config chain (env, shared config,
// SSO, instance role). It resolves the caller account with STS.
func New(ctx context.Context) (*Scanner, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("sts GetCallerIdentity: %w", err)
	}
	return &Scanner{
		Account: aws.ToString(id.Account),
		Region:  cfg.Region,
		EC2:     ec2.NewFromConfig(cfg),
		RDS:     rds.NewFromConfig(cfg),
		DDB:     dynamodb.NewFromConfig(cfg),
		EFS:     efs.NewFromConfig(cfg),
		Backup:  backup.NewFromConfig(cfg),
	}, nil
}

// Inventory enumerates resources of the requested types, backup plan selections
// and each resource's latest recovery point.
func (s *Scanner) Inventory(ctx context.Context, types []coverage.ResourceType) (coverage.Inventory, error) {
	want := map[coverage.ResourceType]bool{}
	for _, t := range types {
		want[t] = true
	}
	all := len(want) == 0

	inv := coverage.Inventory{Account: s.Account, Region: s.Region}

	if all || want[coverage.EBS] {
		rs, err := s.collectEBS(ctx)
		if err != nil {
			return inv, err
		}
		inv.Resources = append(inv.Resources, rs...)
	}
	if all || want[coverage.RDS] {
		rs, err := s.collectRDS(ctx)
		if err != nil {
			return inv, err
		}
		inv.Resources = append(inv.Resources, rs...)
	}
	if all || want[coverage.DynamoDB] {
		rs, err := s.collectDynamoDB(ctx)
		if err != nil {
			return inv, err
		}
		inv.Resources = append(inv.Resources, rs...)
	}
	if all || want[coverage.EFS] {
		rs, err := s.collectEFS(ctx)
		if err != nil {
			return inv, err
		}
		inv.Resources = append(inv.Resources, rs...)
	}

	sels, err := s.collectSelections(ctx)
	if err != nil {
		return inv, err
	}
	inv.Selections = sels

	for _, r := range inv.Resources {
		rps, err := s.collectRecoveryPoints(ctx, r.ARN)
		if err != nil {
			return inv, err
		}
		inv.RecoveryPoints = append(inv.RecoveryPoints, rps...)
	}
	return inv, nil
}

func (s *Scanner) collectEBS(ctx context.Context) ([]coverage.Resource, error) {
	var out []coverage.Resource
	var token *string
	for {
		resp, err := s.EC2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{NextToken: token})
		if err != nil {
			return nil, fmt.Errorf("ec2 DescribeVolumes: %w", err)
		}
		for _, v := range resp.Volumes {
			id := aws.ToString(v.VolumeId)
			tags := map[string]string{}
			for _, t := range v.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			out = append(out, coverage.Resource{
				ARN:  fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s", s.Region, s.Account, id),
				Type: coverage.EBS, ID: id, Tags: tags,
			})
		}
		if resp.NextToken == nil {
			return out, nil
		}
		token = resp.NextToken
	}
}

func (s *Scanner) collectRDS(ctx context.Context) ([]coverage.Resource, error) {
	var out []coverage.Resource
	var marker *string
	for {
		resp, err := s.RDS.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("rds DescribeDBInstances: %w", err)
		}
		for _, db := range resp.DBInstances {
			tags := map[string]string{}
			for _, t := range db.TagList {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			out = append(out, coverage.Resource{
				ARN:  aws.ToString(db.DBInstanceArn),
				Type: coverage.RDS, ID: aws.ToString(db.DBInstanceIdentifier), Tags: tags,
			})
		}
		if resp.Marker == nil {
			return out, nil
		}
		marker = resp.Marker
	}
}

func (s *Scanner) collectDynamoDB(ctx context.Context) ([]coverage.Resource, error) {
	var out []coverage.Resource
	var start *string
	for {
		resp, err := s.DDB.ListTables(ctx, &dynamodb.ListTablesInput{ExclusiveStartTableName: start})
		if err != nil {
			return nil, fmt.Errorf("dynamodb ListTables: %w", err)
		}
		for _, name := range resp.TableNames {
			n := name
			desc, err := s.DDB.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &n})
			if err != nil {
				return nil, fmt.Errorf("dynamodb DescribeTable %s: %w", n, err)
			}
			if desc.Table == nil {
				continue
			}
			out = append(out, coverage.Resource{
				ARN:  aws.ToString(desc.Table.TableArn),
				Type: coverage.DynamoDB, ID: n,
			})
		}
		if resp.LastEvaluatedTableName == nil {
			return out, nil
		}
		start = resp.LastEvaluatedTableName
	}
}

func (s *Scanner) collectEFS(ctx context.Context) ([]coverage.Resource, error) {
	var out []coverage.Resource
	var marker *string
	for {
		resp, err := s.EFS.DescribeFileSystems(ctx, &efs.DescribeFileSystemsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("efs DescribeFileSystems: %w", err)
		}
		for _, fsys := range resp.FileSystems {
			tags := map[string]string{}
			for _, t := range fsys.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			out = append(out, coverage.Resource{
				ARN:  aws.ToString(fsys.FileSystemArn),
				Type: coverage.EFS, ID: aws.ToString(fsys.FileSystemId), Tags: tags,
			})
		}
		if resp.NextMarker == nil {
			return out, nil
		}
		marker = resp.NextMarker
	}
}

func (s *Scanner) collectSelections(ctx context.Context) ([]coverage.Selection, error) {
	var out []coverage.Selection
	var planToken *string
	for {
		plans, err := s.Backup.ListBackupPlans(ctx, &backup.ListBackupPlansInput{NextToken: planToken})
		if err != nil {
			return nil, fmt.Errorf("backup ListBackupPlans: %w", err)
		}
		for _, p := range plans.BackupPlansList {
			planID := aws.ToString(p.BackupPlanId)
			planName := aws.ToString(p.BackupPlanName)
			var selToken *string
			for {
				sels, err := s.Backup.ListBackupSelections(ctx, &backup.ListBackupSelectionsInput{
					BackupPlanId: &planID, NextToken: selToken,
				})
				if err != nil {
					return nil, fmt.Errorf("backup ListBackupSelections: %w", err)
				}
				for _, sm := range sels.BackupSelectionsList {
					selID := aws.ToString(sm.SelectionId)
					got, err := s.Backup.GetBackupSelection(ctx, &backup.GetBackupSelectionInput{
						BackupPlanId: &planID, SelectionId: &selID,
					})
					if err != nil {
						return nil, fmt.Errorf("backup GetBackupSelection: %w", err)
					}
					out = append(out, toSelection(planName, got.BackupSelection))
				}
				if sels.NextToken == nil {
					break
				}
				selToken = sels.NextToken
			}
		}
		if plans.NextToken == nil {
			return out, nil
		}
		planToken = plans.NextToken
	}
}

// toSelection converts an AWS Backup selection into a coverage.Selection. It
// reads both the legacy ListOfTags conditions and the newer Conditions.
// StringEquals parameters, treating every STRINGEQUALS entry as an ANDed tag
// match. This is pure and unit-tested.
func toSelection(planName string, sel *bkptypes.BackupSelection) coverage.Selection {
	cs := coverage.Selection{Plan: planName}
	if sel == nil {
		return cs
	}
	cs.Name = aws.ToString(sel.SelectionName)
	cs.Resources = append(cs.Resources, sel.Resources...)
	cs.NotResources = append(cs.NotResources, sel.NotResources...)
	for _, c := range sel.ListOfTags {
		if c.ConditionType == bkptypes.ConditionTypeStringequals {
			cs.TagConditions = append(cs.TagConditions, coverage.TagCondition{
				Key:   stripTagPrefix(aws.ToString(c.ConditionKey)),
				Value: aws.ToString(c.ConditionValue),
			})
		}
	}
	if sel.Conditions != nil {
		for _, p := range sel.Conditions.StringEquals {
			cs.TagConditions = append(cs.TagConditions, coverage.TagCondition{
				Key:   stripTagPrefix(aws.ToString(p.ConditionKey)),
				Value: aws.ToString(p.ConditionValue),
			})
		}
	}
	return cs
}

// stripTagPrefix drops the "aws:ResourceTag/" prefix that Backup uses on
// Conditions keys so the key matches a plain resource tag key.
func stripTagPrefix(k string) string {
	const p = "aws:ResourceTag/"
	if len(k) > len(p) && k[:len(p)] == p {
		return k[len(p):]
	}
	return k
}

func (s *Scanner) collectRecoveryPoints(ctx context.Context, arn string) ([]coverage.RecoveryPoint, error) {
	var out []coverage.RecoveryPoint
	var token *string
	for {
		resp, err := s.Backup.ListRecoveryPointsByResource(ctx, &backup.ListRecoveryPointsByResourceInput{
			ResourceArn: &arn, NextToken: token,
		})
		if err != nil {
			// A resource with no backups returns an empty list, not an error;
			// surface real errors to the caller.
			return nil, fmt.Errorf("backup ListRecoveryPointsByResource %s: %w", arn, err)
		}
		for _, rp := range resp.RecoveryPoints {
			if rp.CreationDate == nil {
				continue
			}
			out = append(out, coverage.RecoveryPoint{ResourceARN: arn, CompletedAt: *rp.CreationDate})
		}
		if resp.NextToken == nil {
			return out, nil
		}
		token = resp.NextToken
	}
}
