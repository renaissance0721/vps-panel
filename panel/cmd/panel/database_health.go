package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/databasehealth"
)

func runDatabaseHealthCheck(ctx context.Context, dataDir string, output io.Writer) (bool, error) {
	report, err := databasehealth.CheckPath(ctx, databasehealth.DatabasePath(dataDir))
	if err != nil {
		return false, err
	}
	writeDatabaseHealthReport(output, report)
	return report.Healthy(), nil
}

func runDatabaseRepair(ctx context.Context, dataDir string, output io.Writer, now time.Time) (bool, error) {
	result, err := databasehealth.Repair(ctx, databasehealth.DatabasePath(dataDir), now)
	if len(result.Before.IntegrityMessages) != 0 {
		fmt.Fprintln(output, "Before:")
		writeDatabaseHealthSummary(output, result.Before)
	}
	if result.BackupPath != "" {
		fmt.Fprintf(output, "\nBackup: %s\n", result.BackupPath)
	}
	if len(result.Repaired) != 0 {
		fmt.Fprintln(output, "\nRepaired:")
		for _, repaired := range result.Repaired {
			fmt.Fprintf(output, "  %s orphan rows: %d\n", repaired.Table, repaired.Count)
		}
	}
	if len(result.After.IntegrityMessages) != 0 {
		fmt.Fprintln(output, "\nAfter:")
		writeDatabaseHealthSummary(output, result.After)
	}
	if !result.Committed && len(result.Repaired) != 0 {
		fmt.Fprintln(output, "\nRepair transaction: ROLLED BACK; no database rows were changed.")
	}
	if result.ManualNeeded {
		fmt.Fprintln(output, "\nManual intervention required; business data was not modified.")
	}
	if err != nil {
		fmt.Fprintln(output, "\nDatabase health: FAILED")
		return false, err
	}
	if result.After.Healthy() {
		fmt.Fprintln(output, "\nDatabase health: OK")
		return true, nil
	}
	fmt.Fprintln(output, "\nDatabase health: FAILED")
	return false, nil
}

func writeDatabaseHealthReport(output io.Writer, report databasehealth.Report) {
	writeDatabaseHealthSummary(output, report)
	if report.ForeignKeys.Total != 0 {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "[foreign-key]")
		fmt.Fprintln(output, databasehealth.FormatForeignKeyViolations(report.ForeignKeys))
	}
	category := ""
	for _, check := range report.Checks {
		if check.Category != category {
			category = check.Category
			fmt.Fprintf(output, "\n[%s]\n", category)
		}
		fmt.Fprintf(output, "%s: %d\n", check.Name, check.Count)
	}
	if report.Healthy() {
		fmt.Fprintln(output, "\nDatabase health: OK")
	} else {
		fmt.Fprintln(output, "\nDatabase health: FAILED")
	}
}

func writeDatabaseHealthSummary(output io.Writer, report databasehealth.Report) {
	if report.IntegrityOK() {
		fmt.Fprintln(output, "Database integrity: OK")
	} else {
		message := strings.Join(report.IntegrityMessages, "; ")
		if message == "" {
			message = "check did not return a result"
		}
		fmt.Fprintf(output, "Database integrity: FAILED (%s)\n", message)
	}
	fmt.Fprintf(output, "Foreign keys: %d issue(s)\n", report.ForeignKeys.Total)
}
