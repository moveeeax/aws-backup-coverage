// Package report renders coverage findings as a human table, JSON, or SARIF.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/moveeeax/aws-backup-coverage/internal/coverage"
)

// JSON writes the report object as indented JSON.
func JSON(w io.Writer, rep coverage.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// Table writes an aligned, human-readable table followed by a summary line.
func Table(w io.Writer, rep coverage.Report) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tTYPE\tRESOURCE\tDETAIL")
	for _, f := range rep.Findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Status, f.Resource.Type, f.Resource.ID, f.Reason)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d resources: %d covered, %d stale, %d uncovered (stale-after %s)\n",
		rep.Summary.Total, rep.Summary.Covered, rep.Summary.Stale, rep.Summary.Uncovered, rep.StaleAfter)
	return err
}

// SARIF 2.1.0 minimal schema.
type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	ShortDescription sarifText         `json:"shortDescription"`
	DefaultConfig    sarifRuleConfig   `json:"defaultConfiguration"`
	Properties       map[string]string `json:"properties,omitempty"`
}

type sarifRuleConfig struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

const infoURI = "https://github.com/moveeeax/aws-backup-coverage"

func levelFor(s coverage.Status) string {
	switch s {
	case coverage.StatusUncovered:
		return "error"
	case coverage.StatusStale:
		return "warning"
	default:
		return "note"
	}
}

// SARIF writes findings as a SARIF 2.1.0 log. Only uncovered and stale findings
// become results; compliant resources are omitted so a clean run has an empty
// results array.
func SARIF(w io.Writer, rep coverage.Report) error {
	log := sarifLog{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "aws-backup-coverage",
				InformationURI: infoURI,
				Rules: []sarifRule{
					{ID: "backup-uncovered", Name: "BackupUncovered",
						ShortDescription: sarifText{Text: "Resource is not selected by any AWS Backup plan"},
						DefaultConfig:    sarifRuleConfig{Level: "error"}},
					{ID: "backup-stale", Name: "BackupStale",
						ShortDescription: sarifText{Text: "Resource is covered but its latest recovery point is missing or overdue"},
						DefaultConfig:    sarifRuleConfig{Level: "warning"}},
				},
			}},
			Results: []sarifResult{},
		}},
	}
	for _, f := range rep.Findings {
		if f.Status == coverage.StatusCovered {
			continue
		}
		ruleID := "backup-stale"
		if f.Status == coverage.StatusUncovered {
			ruleID = "backup-uncovered"
		}
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID:  ruleID,
			Level:   levelFor(f.Status),
			Message: sarifText{Text: fmt.Sprintf("%s (%s): %s", f.Resource.ID, f.Resource.Type, f.Reason)},
			Locations: []sarifLocation{{
				PhysicalLocation: sarifPhysical{ArtifactLocation: sarifArtifact{URI: f.Resource.ARN}},
			}},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
