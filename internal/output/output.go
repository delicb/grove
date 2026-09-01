package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/del-boy/grove/internal/model"
)

func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func WriteJSONError(writer io.Writer, command string, err error) error {
	return WriteJSON(writer, model.NewErrorDocument(command, DomainError(err)))
}

func WriteHumanError(writer io.Writer, err error) error {
	domainErr := DomainError(err)
	if _, writeErr := fmt.Fprintf(writer, "Error: %s\n", domainErr.Message); writeErr != nil {
		return writeErr
	}
	if len(domainErr.Details) == 0 {
		if domainErr.Err == nil {
			return nil
		}
		return writeErrorDetail(writer, "cause", domainErr.Err)
	}
	keys := make([]string, 0, len(domainErr.Details))
	for key := range domainErr.Details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if writeErr := writeErrorDetail(writer, key, domainErr.Details[key]); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func writeErrorDetail(writer io.Writer, key string, value any) error {
	formatted := fmt.Sprintf("%v", value)
	if !strings.Contains(formatted, "\n") {
		_, err := fmt.Fprintf(writer, "  %s: %s\n", key, formatted)
		return err
	}
	if _, err := fmt.Fprintf(writer, "  %s:\n", key); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimRight(formatted, "\n"), "\n") {
		if _, err := fmt.Fprintf(writer, "    %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func DomainError(err error) *model.Error {
	var domainErr *model.Error
	if errors.As(err, &domainErr) {
		return domainErr
	}
	return model.NewError(model.ErrorInternal, model.ExitInternal, "Grove could not complete the command.", err)
}

func ExitCode(err error) int {
	if err == nil {
		return model.ExitSuccess
	}
	return DomainError(err).ExitCode
}

func ResultExitCode(failures []model.Issue, bootstrap *model.BootstrapResult) int {
	if bootstrap != nil && (bootstrap.State == model.BootstrapStateFailed || bootstrap.State == model.BootstrapStateInterrupted) {
		return model.ExitBootstrap
	}
	if len(failures) != 0 {
		return model.ExitPartial
	}
	return model.ExitSuccess
}

func WriteCreate(writer io.Writer, data model.CreateData) error {
	branch := worktreeBranch(data.Worktree)
	if _, err := fmt.Fprintf(
		writer,
		"Created %s\nPath: %s\nBranch: %s\nCreator: %s\nBootstrap: %s\nSize: %s\n",
		data.Worktree.Name,
		data.Worktree.Path,
		branch,
		data.Worktree.CreatorAgent,
		data.Bootstrap.State,
		formatSizePointer(data.Worktree.SizeBytes),
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "Run grove touch %q when you resume work.\n", data.Worktree.Path)
	return err
}

func WriteList(writer io.Writer, data model.ListData) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "REPOSITORY\tNAME\tBRANCH\tCREATOR\tACTIVITY\tSTATE\tSIZE\tSIZE STATUS\tPATH"); err != nil {
		return err
	}
	for _, worktree := range data.Worktrees {
		if _, err := fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			worktree.Repository,
			worktree.Name,
			worktreeBranch(worktree),
			worktree.CreatorAgent,
			formatTime(worktree.LastGroveActivityAt),
			worktree.State,
			formatSizePointer(worktree.SizeBytes),
			sizeStatus(worktree),
			worktree.Path,
		); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"Total: %d worktrees, %s, %d unknown sizes\n",
		len(data.Worktrees),
		FormatSize(data.Summary.SizeBytes),
		data.Summary.UnknownSizeCount,
	)
	return err
}

func WriteTouch(writer io.Writer, data model.TouchData) error {
	_, err := fmt.Fprintf(
		writer,
		"Updated %s\nPrevious activity: %s\nActivity: %s\n",
		data.Worktree.Path,
		formatTime(data.PreviousActivityAt),
		formatTime(data.Worktree.LastGroveActivityAt),
	)
	return err
}

func WriteStats(writer io.Writer, data model.StatsData) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	rows := [][2]string{
		{"Active", fmt.Sprint(data.Active)},
		{"Missing", fmt.Sprint(data.Missing)},
		{"Manual review", fmt.Sprint(data.ManualReview)},
		{"Repositories", fmt.Sprint(data.RepositoryCount)},
		{"Size", FormatSize(data.SizeBytes)},
		{"Unknown sizes", fmt.Sprint(data.UnknownSizeCount)},
		{"Incomplete sizes", fmt.Sprint(data.IncompleteSizeCount)},
		{"Size complete", fmt.Sprint(data.SizeComplete)},
		{"Calculated at", formatTime(data.CalculatedAt)},
		{"Oldest measurement", formatOptionalTime(data.OldestMeasurementAt)},
		{"Newest measurement", formatOptionalTime(data.NewestMeasurementAt)},
	}
	if data.Removed != nil {
		rows = append(rows, [2]string{"Removed", fmt.Sprint(*data.Removed)})
	}
	if data.CreateFailed != nil {
		rows = append(rows, [2]string{"Create failed", fmt.Sprint(*data.CreateFailed)})
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(table, "%s:\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	return table.Flush()
}

func WriteCleanup(writer io.Writer, data model.CleanupData) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "ACTION\tREASON\tACTIVITY\tCUTOFF\tSIZE\tPATH"); err != nil {
		return err
	}
	for _, item := range data.Items {
		if _, err := fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Action,
			item.Reason,
			formatTime(item.Worktree.LastGroveActivityAt),
			formatTime(data.CutoffAt),
			formatSizePointer(item.FinalSizeBytes),
			item.Worktree.Path,
		); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if err := WriteCleanupSummary(writer, data.Summary); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, "Run grove touch <name-or-absolute-path> when you resume work.")
	return err
}

func WriteCleanupSummary(writer io.Writer, summary model.CleanupSummary) error {
	_, err := fmt.Fprintf(
		writer,
		"Cleanup: %d candidates, %d deleted, %d skipped, %d failed\n",
		summary.Candidate,
		summary.Deleted,
		summary.Skipped,
		summary.Failed,
	)
	return err
}

func WriteConfigShow(writer io.Writer, data model.ConfigShowData, dataDirSource model.ValueSource) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "VALUE\tSOURCE\tSETTING"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(table, "%s\t%s\troot\n", data.Root, data.RootSource); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(table, "%s\t%s\tbootstrap_script\n", data.BootstrapScript, data.BootstrapScriptSource); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(table, "%s\t%s\tdata_dir\n", data.DataDir, dataDirSource); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(table, "%s\t-\tconfig_path\n", formatOptionalString(data.ConfigPath)); err != nil {
		return err
	}
	return table.Flush()
}

func WriteConfigPath(writer io.Writer, data model.ConfigPathData) error {
	_, err := fmt.Fprintln(writer, formatOptionalString(data.ConfigPath))
	return err
}

func WriteIssues(writer io.Writer, warnings, failures []model.Issue) error {
	for _, issue := range warnings {
		if err := writeIssue(writer, "Warning", issue); err != nil {
			return err
		}
	}
	for _, issue := range failures {
		if err := writeIssue(writer, "Failure", issue); err != nil {
			return err
		}
	}
	return nil
}

func FormatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value := float64(bytes)
	unit := "B"
	for _, candidate := range units {
		value /= 1024
		unit = candidate
		if math.Abs(value) < 1024 {
			break
		}
	}
	formatted := fmt.Sprintf("%.1f", value)
	formatted = strings.TrimSuffix(formatted, ".0")
	return formatted + " " + unit
}

func worktreeBranch(worktree model.Worktree) string {
	if worktree.Branch != nil {
		return *worktree.Branch
	}
	if worktree.DetachedCommit != nil {
		commit := *worktree.DetachedCommit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		return "detached@" + commit
	}
	return "-"
}

func sizeStatus(worktree model.Worktree) string {
	if worktree.SizeBytes == nil {
		return "unknown"
	}
	if worktree.SizeComplete {
		return "complete"
	}
	return "incomplete"
}

func formatSizePointer(value *int64) string {
	if value == nil {
		return "-"
	}
	return FormatSize(*value)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return formatTime(*value)
}

func formatOptionalString(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}

func writeIssue(writer io.Writer, label string, issue model.Issue) error {
	if issue.Path != nil {
		_, err := fmt.Fprintf(writer, "%s [%s]: %s (%s)\n", label, issue.Code, issue.Message, *issue.Path)
		return err
	}
	_, err := fmt.Fprintf(writer, "%s [%s]: %s\n", label, issue.Code, issue.Message)
	return err
}
