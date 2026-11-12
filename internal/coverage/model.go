// Package coverage models AWS Backup coverage and classifies backup-eligible
// resources as covered, uncovered, or stale. It is deliberately free of any AWS
// SDK dependency so the classification logic can be unit-tested with fixtures.
package coverage

import "time"

// ResourceType is a backup-eligible AWS resource kind.
type ResourceType string

const (
	EBS      ResourceType = "ebs"
	RDS      ResourceType = "rds"
	DynamoDB ResourceType = "dynamodb"
	EFS      ResourceType = "efs"
)

// AllTypes is the set of resource kinds this tool understands.
var AllTypes = []ResourceType{EBS, RDS, DynamoDB, EFS}

// Resource is a single backup-eligible resource discovered in an account/region.
// Region is optional in an inventory file; when empty the inventory's Region
// applies.
type Resource struct {
	ARN    string            `json:"arn"`
	Type   ResourceType      `json:"type"`
	ID     string            `json:"id"`
	Region string            `json:"region,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

// TagCondition is a single STRINGEQUALS tag match on a backup plan selection.
// Multiple conditions on one selection are ANDed, matching AWS Backup's
// Conditions.StringEquals semantics.
type TagCondition struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Selection is one assignment of resources to a backup plan. A resource is
// selected when it is not excluded by NotResources AND (it matches one of the
// Resources ARN patterns OR it satisfies every TagCondition). A selection with
// neither Resources nor TagConditions matches nothing.
//
// Backup plans are regional, so a selection only covers resources in its own
// Region. An empty Region falls back to the inventory's Region; if either side
// is still unknown the region check is skipped.
type Selection struct {
	Plan          string         `json:"plan"`
	Name          string         `json:"name,omitempty"`
	Region        string         `json:"region,omitempty"`
	Resources     []string       `json:"resources,omitempty"`
	NotResources  []string       `json:"notResources,omitempty"`
	TagConditions []TagCondition `json:"tagConditions,omitempty"`
}

// RecoveryPoint is the completion time of a backup for a given resource ARN.
type RecoveryPoint struct {
	ResourceARN string    `json:"resourceArn"`
	CompletedAt time.Time `json:"completedAt"`
}

// Inventory is everything needed to compute coverage for one account and one
// or more regions. A single-region inventory sets Region; a multi-region one
// lists Regions and sets Region on every resource and selection.
type Inventory struct {
	Account        string          `json:"account,omitempty"`
	Region         string          `json:"region,omitempty"`
	Regions        []string        `json:"regions,omitempty"`
	Resources      []Resource      `json:"resources"`
	Selections     []Selection     `json:"selections"`
	RecoveryPoints []RecoveryPoint `json:"recoveryPoints"`
}

// Status is the coverage classification of a single resource.
type Status string

const (
	StatusCovered   Status = "covered"
	StatusUncovered Status = "uncovered"
	StatusStale     Status = "stale"
)

// severity orders statuses for --fail-on threshold comparisons.
func (s Status) severity() int {
	switch s {
	case StatusUncovered:
		return 2
	case StatusStale:
		return 1
	default:
		return 0
	}
}

// Finding is the classification result for one resource.
type Finding struct {
	Resource            Resource   `json:"resource"`
	Status              Status     `json:"status"`
	Reason              string     `json:"reason"`
	Plans               []string   `json:"plans,omitempty"`
	LatestRecoveryPoint *time.Time `json:"latestRecoveryPoint,omitempty"`
}

// Summary is the per-status count across all findings.
type Summary struct {
	Total     int `json:"total"`
	Covered   int `json:"covered"`
	Uncovered int `json:"uncovered"`
	Stale     int `json:"stale"`
}

// RegionSummary is the per-status count for the findings of one region.
type RegionSummary struct {
	Region string `json:"region"`
	Summary
}

// Report bundles the inventory metadata, findings and summary for output.
// Regions holds one summary per region present in the findings.
type Report struct {
	Account    string          `json:"account,omitempty"`
	Region     string          `json:"region,omitempty"`
	Findings   []Finding       `json:"findings"`
	Summary    Summary         `json:"summary"`
	Regions    []RegionSummary `json:"regions,omitempty"`
	StaleAfter string          `json:"staleAfter"`
}
