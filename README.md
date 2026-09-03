# aws-backup-coverage

[![ci](https://github.com/moveeeax/aws-backup-coverage/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/aws-backup-coverage/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.24%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Find the AWS resources that AWS Backup silently forgot.

AWS Backup centralizes backups behind **plans** and **selections** (by tag or by
resource ARN). Nothing stops a freshly created EBS volume, RDS instance,
DynamoDB table or EFS filesystem from landing outside every selection — so teams
say "we have AWS Backup" while whole resources sit with **zero recovery points**.
`aws-backup-coverage` is a read-only CLI that answers two questions:

1. Which backup-eligible resources are **not covered** by any backup plan?
2. Which are covered but **haven't backed up recently** (stale)?

## How it works

1. Enumerate backup-eligible resources in the account/region: EBS volumes
   (`ec2:DescribeVolumes`), RDS instances (`rds:DescribeDBInstances`), DynamoDB
   tables (`dynamodb:ListTables`/`DescribeTable`), EFS filesystems
   (`elasticfilesystem:DescribeFileSystems`).
2. Read every backup plan and its selections
   (`backup:ListBackupPlans`/`ListBackupSelections`/`GetBackupSelection`),
   normalizing both ARN patterns and tag conditions (`ListOfTags` and the newer
   `Conditions.StringEquals`).
3. For each resource, look up its latest recovery point
   (`backup:ListRecoveryPointsByResource`) and classify it:
   - **uncovered** — no selection matches it;
   - **stale** — covered, but the latest recovery point is missing or older than
     `--stale-after`;
   - **covered** — covered with a recent recovery point.

Every call is a `List`/`Describe`/`Get` — the tool never mutates anything and is
safe to run in CI. The ARN/tag matcher supports AWS-style `*` wildcards and
honors a selection's `NotResources` exclusions.

## Install

```sh
go install github.com/moveeeax/aws-backup-coverage@latest
```

Or clone and `go build -o aws-backup-coverage .`.

## Usage

```sh
# Check every backup-eligible resource in the current account/region
aws-backup-coverage check

# Only RDS and EBS, JSON output
aws-backup-coverage check --types rds,ebs --output json > cov.json

# Fail CI when anything is uncovered, emit SARIF for code scanning
aws-backup-coverage check --fail-on uncovered --output sarif > backup.sarif

# Treat a recovery point older than 3 days as stale
aws-backup-coverage check --stale-after 3d
```

Credentials come from the standard AWS chain (env vars, shared config, SSO,
instance role). The least-privilege policy the tool needs ships as
[`iam-policy.json`](iam-policy.json).

### Offline / demo mode

`--from-file` runs the classifier against an inventory JSON instead of calling
AWS — handy for tests, demos, and reproducing a report:

```console
$ aws-backup-coverage check --from-file examples/inventory.json
STATUS     TYPE      RESOURCE       DETAIL
uncovered  dynamodb  sessions       no backup plan selection matches this resource
uncovered  ebs       vol-deadbeef   no backup plan selection matches this resource
stale      efs       fs-01aa22bb    covered by daily-35day but has no recovery point
stale      rds       payments-prod  latest recovery point 2026-07-01T04:00:00Z is older than 168h0m0s
covered    ebs       vol-0a1b2c3d   covered by daily-35day

5 resources: 1 covered, 2 stale, 2 uncovered (stale-after 168h0m0s)
```

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--types` | all | comma list of `ebs,rds,dynamodb,efs` |
| `--output`, `-o` | `table` | `table`, `json`, or `sarif` |
| `--stale-after` | `7d` | age at which a covered resource is stale (`24h`, `3d`, …) |
| `--fail-on` | `none` | exit non-zero at `stale` or `uncovered` |
| `--from-file` | — | classify an inventory JSON instead of calling AWS |

## Exit codes

- `0` — success, no threshold crossed.
- `3` — a `--fail-on` threshold was crossed (use this to gate a pipeline).
- `1` — an operational error (bad flags, AWS error, unreadable file).

## License

MIT — see [LICENSE](LICENSE).
