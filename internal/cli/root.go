package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"skillctl/internal/config"
	"skillctl/internal/inventory"
	"skillctl/internal/launchagent"
	"skillctl/internal/model"
	"skillctl/internal/service"
	statestore "skillctl/internal/state"
	"skillctl/internal/tui"
	"skillctl/internal/watcher"
)

const Version = "0.1.0"

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

type options struct {
	configPath string
	statePath  string
	cwd        string
	json       bool
}

func Execute() error {
	root, err := newRoot()
	if err != nil {
		return err
	}
	err = root.Execute()
	if err == nil {
		return nil
	}
	var configErr *config.Error
	if errors.As(err, &configErr) {
		return &ExitError{Code: 2, Err: err}
	}
	return err
}

func newRoot() (*cobra.Command, error) {
	configPath, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	statePath, err := statestore.DefaultPath()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	values := &options{configPath: configPath, statePath: statePath, cwd: cwd}
	root := &cobra.Command{
		Use:           "skillctl",
		Short:         "Control which agent skills enter the model context",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context(), values, false)
		},
	}
	root.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return &ExitError{Code: 2, Err: err}
	})
	root.PersistentFlags().StringVar(&values.configPath, "config", values.configPath, "configuration file")
	root.PersistentFlags().StringVar(&values.statePath, "state-file", values.statePath, "local state file")
	root.PersistentFlags().StringVarP(&values.cwd, "cwd", "C", values.cwd, "working directory used for skill discovery")
	root.PersistentFlags().BoolVar(&values.json, "json", false, "emit machine-readable JSON")

	root.AddCommand(
		newTUICommand(values),
		newInitCommand(values),
		newListCommand(values),
		newStatusCommand(values),
		newSetCommand(values),
		newStateAliasCommand(values, "allow", model.StateImplicit),
		newStateAliasCommand(values, "manual", model.StateManual),
		newStateAliasCommand(values, "disable", model.StateDisabled),
		newSyncCommand(values),
		newDoctorCommand(values),
		newRestoreCommand(values),
		newWatchCommand(values),
		&cobra.Command{
			Use:   "version",
			Short: "Print the skillctl version",
			Args:  noArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if values.json {
					return printJSON(map[string]string{"version": Version})
				}
				fmt.Fprintln(cmd.OutOrStdout(), Version)
				return nil
			},
		},
	)
	return root, nil
}

func newTUICommand(values *options) *cobra.Command {
	var project bool
	command := &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive skill manager",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context(), values, project)
		},
	}
	command.Flags().BoolVar(&project, "project", false, "allow staging changes to project skills")
	return command
}

func runTUI(ctx context.Context, values *options, project bool) error {
	if values.json {
		return &ExitError{Code: 2, Err: errors.New("tui does not support --json; use skillctl list --json")}
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return &ExitError{Code: 2, Err: errors.New("interactive terminal required; use skillctl list or skillctl list --json")}
	}
	manager := values.manager()
	if _, exists, err := config.LoadOrDefault(manager.ConfigPath); err != nil {
		return err
	} else if !exists {
		if _, _, err := manager.Init(ctx, false, false); err != nil {
			return err
		}
	}
	return tui.Run(ctx, tui.Options{Manager: manager, Project: project})
}

func (o *options) manager() service.Manager {
	return service.Manager{ConfigPath: o.configPath, StatePath: o.statePath, CWD: o.cwd}
}

func newInitCommand(values *options) *cobra.Command {
	var force bool
	var apply bool
	command := &cobra.Command{
		Use:   "init",
		Short: "Create the private skillctl configuration",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, report, err := values.manager().Init(cmd.Context(), force, apply)
			if err != nil {
				return err
			}
			if values.json {
				return printJSON(map[string]any{"config_path": values.configPath, "config": cfg, "sync": report})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", values.configPath)
			fmt.Fprintln(cmd.OutOrStdout(), "Implicit whitelist is empty; user and plugin skills default to manual.")
			if report != nil {
				printSync(cmd, *report)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Run skillctl sync --dry-run, then skillctl sync.")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "replace an existing skillctl config")
	command.Flags().BoolVar(&apply, "apply", false, "apply the initial manual-only policy immediately")
	return command
}

func newListCommand(values *options) *cobra.Command {
	var project bool
	var state string
	var scope string
	var source string
	var drift bool
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List discovered skills and their policy state",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			items, warnings, err := values.manager().List(cmd.Context(), project)
			if err != nil {
				return err
			}
			filter, err := listFilter(state, scope, source, drift)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			groups := inventory.GroupStatuses(inventory.Apply(items, filter))
			if values.json {
				return printJSON(map[string]any{"groups": groups, "warnings": warnings})
			}
			printGroups(cmd, groups)
			printWarnings(cmd, warnings)
			return nil
		},
	}
	command.Flags().BoolVar(&project, "project", false, "include project skills in management status")
	command.Flags().StringVar(&state, "state", "", "filter by actual state: implicit, manual, or disabled")
	command.Flags().BoolVar(&drift, "drift", false, "show only skills whose actual and desired states differ")
	command.Flags().StringVar(&scope, "scope", "", "filter by scope: system, user, plugin, repo, admin, or other")
	command.Flags().StringVar(&source, "source", "", "filter by source name")
	return command
}

func newStatusCommand(values *options) *cobra.Command {
	var project bool
	command := &cobra.Command{
		Use:   "status <skill>",
		Short: "Show one skill's current and desired state",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, warnings, err := values.manager().List(cmd.Context(), project)
			if err != nil {
				return err
			}
			item, err := resolveStatus(items, args[0])
			if err != nil {
				return err
			}
			if values.json {
				return printJSON(map[string]any{"skill": item, "warnings": warnings})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ID:      %s\n", item.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Actual:  %s\n", item.Actual)
			fmt.Fprintf(cmd.OutOrStdout(), "Desired: %s\n", item.Desired)
			fmt.Fprintf(cmd.OutOrStdout(), "Scope:   %s\n", item.Scope)
			fmt.Fprintf(cmd.OutOrStdout(), "Path:    %s\n", item.Path)
			printWarnings(cmd, warnings)
			return nil
		},
	}
	command.Flags().BoolVar(&project, "project", false, "allow project skill lookup")
	return command
}

func newSetCommand(values *options) *cobra.Command {
	var noSync bool
	var project bool
	command := &cobra.Command{
		Use:   "set <skill> <implicit|manual|disabled>",
		Short: "Set and immediately apply a skill policy",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			desired := model.InvocationState(args[1])
			return runSet(cmd, values, args[0], desired, noSync, project)
		},
	}
	command.Flags().BoolVar(&noSync, "no-sync", false, "update config without applying the change")
	command.Flags().BoolVar(&project, "project", false, "allow modifying a project skill")
	return command
}

func newStateAliasCommand(values *options, name string, desired model.InvocationState) *cobra.Command {
	var noSync bool
	var project bool
	command := &cobra.Command{
		Use:   name + " <skill>",
		Short: "Set a skill to " + string(desired),
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSet(cmd, values, args[0], desired, noSync, project)
		},
	}
	command.Flags().BoolVar(&noSync, "no-sync", false, "update config without applying the change")
	command.Flags().BoolVar(&project, "project", false, "allow modifying a project skill")
	return command
}

func runSet(cmd *cobra.Command, values *options, selector string, desired model.InvocationState, noSync, project bool) error {
	if !desired.Valid() {
		return &ExitError{Code: 2, Err: fmt.Errorf("invalid invocation state %q", desired)}
	}
	skill, report, err := values.manager().Set(cmd.Context(), selector, desired, noSync, project)
	if values.json {
		if printErr := printJSON(map[string]any{"skill": skill, "desired": desired, "sync": report, "error": errorString(err)}); printErr != nil {
			return printErr
		}
	} else {
		if skill.ID != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s -> %s\n", skill.ID, desired)
		}
		if report != nil {
			printSync(cmd, *report)
		}
	}
	if err != nil {
		if report != nil && report.Conflicts > 0 {
			return &ExitError{Code: 4, Err: err}
		}
		return err
	}
	return nil
}

func newSyncCommand(values *options) *cobra.Command {
	var dryRun bool
	var project bool
	command := &cobra.Command{
		Use:   "sync",
		Short: "Reconcile installed skills with the active profile",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := values.manager().Sync(cmd.Context(), service.SyncOptions{DryRun: dryRun, Project: project})
			if values.json {
				if printErr := printJSON(report); printErr != nil {
					return printErr
				}
			} else {
				printSync(cmd, report)
			}
			if err != nil {
				if report.Conflicts > 0 {
					return &ExitError{Code: 4, Err: err}
				}
				return err
			}
			return nil
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show changes without writing files")
	command.Flags().BoolVar(&project, "project", false, "also manage project skills")
	return command
}

func newDoctorCommand(values *options) *cobra.Command {
	var project bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check for policy drift without changing files",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := values.manager().Sync(cmd.Context(), service.SyncOptions{DryRun: true, Project: project})
			if values.json {
				if printErr := printJSON(report); printErr != nil {
					return printErr
				}
			} else {
				printSync(cmd, report)
			}
			if err != nil {
				return &ExitError{Code: 4, Err: err}
			}
			if report.Changed > 0 {
				return &ExitError{Code: 3, Err: fmt.Errorf("policy drift detected for %d skills", report.Changed)}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&project, "project", false, "also check project skills")
	return command
}

func newRestoreCommand(values *options) *cobra.Command {
	var all bool
	var dryRun bool
	command := &cobra.Command{
		Use:   "restore [skill...]",
		Short: "Restore policies captured before skillctl management",
		Args: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return &ExitError{Code: 2, Err: errors.New("provide a skill selector or use --all")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			selectors := make([]string, 0, len(args))
			for _, selector := range args {
				skill, err := values.manager().Resolve(cmd.Context(), selector)
				if err != nil {
					selectors = append(selectors, selector)
					continue
				}
				selectors = append(selectors, skill.ID)
			}
			report, err := values.manager().Restore(cmd.Context(), selectors, all, dryRun)
			if values.json {
				if printErr := printJSON(report); printErr != nil {
					return printErr
				}
			} else {
				for _, id := range report.Restored {
					fmt.Fprintf(cmd.OutOrStdout(), "restored %s\n", id)
				}
				for _, conflict := range report.Conflicts {
					fmt.Fprintf(cmd.ErrOrStderr(), "conflict: %s\n", conflict)
				}
			}
			if err != nil {
				if len(report.Conflicts) > 0 {
					return &ExitError{Code: 4, Err: err}
				}
				return err
			}
			return nil
		},
	}
	command.Flags().BoolVar(&all, "all", false, "restore every skill managed by skillctl")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be restored")
	return command
}

func newWatchCommand(values *options) *cobra.Command {
	var interval time.Duration
	command := &cobra.Command{
		Use:   "watch",
		Short: "Watch installed skills and reconcile policy changes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			watch := watcher.Watcher{ConfigPath: values.configPath, StatePath: values.statePath, CWD: values.cwd, Interval: interval}
			return watch.Run(ctx, func(runContext context.Context) error {
				report, err := values.manager().Sync(runContext, service.SyncOptions{})
				if values.json {
					if printErr := printJSON(report); printErr != nil {
						return printErr
					}
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] ", time.Now().Format(time.RFC3339))
					printSync(cmd, report)
				}
				return err
			})
		},
	}
	command.PersistentFlags().DurationVar(&interval, "interval", 5*time.Second, "filesystem polling interval")
	command.AddCommand(newWatchInstallCommand(values, &interval), newWatchUninstallCommand(values), newWatchStatusCommand(values))
	return command
}

func newWatchInstallCommand(values *options, interval *time.Duration) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the watcher as a macOS LaunchAgent",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			definition := launchagent.Definition{
				Executable:      executable,
				ConfigPath:      values.configPath,
				StatePath:       values.statePath,
				Interval:        interval.String(),
				LogPath:         filepath.Join(filepath.Dir(values.statePath), "watch.log"),
				WorkingDir:      home,
				EnvironmentPath: os.Getenv("PATH"),
			}
			if dryRun {
				data, renderErr := launchagent.Render(definition)
				if renderErr != nil {
					return renderErr
				}
				if values.json {
					return printJSON(map[string]any{"installed": false, "dry_run": true, "plist": string(data)})
				}
				_, renderErr = cmd.OutOrStdout().Write(data)
				return renderErr
			}
			path, err := launchagent.Install(definition)
			if err != nil {
				return err
			}
			if values.json {
				return printJSON(map[string]any{"installed": true, "path": path})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s\n", path)
			return nil
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the LaunchAgent without installing it")
	return command
}

func newWatchUninstallCommand(values *options) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the macOS LaunchAgent watcher",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := launchagent.Uninstall()
			if err != nil {
				return err
			}
			if values.json {
				return printJSON(map[string]any{"installed": false, "path": path})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", path)
			return nil
		},
	}
}

func newWatchStatusCommand(values *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the LaunchAgent watcher is installed",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, path, err := launchagent.Status()
			if err != nil {
				return err
			}
			if values.json {
				return printJSON(map[string]any{"installed": installed, "path": path})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed: %t\nPath: %s\n", installed, path)
			return nil
		},
	}
}

func resolveStatus(items []service.SkillStatus, selector string) (service.SkillStatus, error) {
	var matches []service.SkillStatus
	for _, item := range items {
		if item.ID == selector || item.Name == selector || item.Path == selector {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return service.SkillStatus{}, fmt.Errorf("skill %q was not found", selector)
	}
	if len(matches) > 1 {
		var ids []string
		for _, item := range matches {
			ids = append(ids, item.ID)
		}
		sort.Strings(ids)
		return service.SkillStatus{}, fmt.Errorf("skill %q is ambiguous; use one of: %s", selector, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func printSync(cmd *cobra.Command, report model.SyncReport) {
	mode := "applied"
	if report.DryRun {
		mode = "planned"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Scanned %d, managed %d, %s %d, conflicts %d.\n", report.Scanned, report.Managed, mode, report.Changed, report.Conflicts)
	for _, change := range report.Changes {
		line := fmt.Sprintf("%s: %s -> %s", change.SkillID, change.From, change.To)
		if change.Message != "" {
			line += " (" + change.Message + ")"
		}
		fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	printWarnings(cmd, report.Warnings)
}

func printWarnings(cmd *cobra.Command, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
	}
}

func printGroups(cmd *cobra.Command, groups []inventory.Group) {
	if len(groups) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No skills matched.")
		return
	}
	for groupIndex, group := range groups {
		if groupIndex > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s · %s\n", inventory.CategoryTitle(group.Category), group.Label)
		fmt.Fprintln(cmd.OutOrStdout(), inventory.SummaryLine(group.Summary))
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tACTUAL\tDESIRED\tMANAGED\tPATH")
		for _, item := range group.Skills {
			managed := "no"
			if item.Managed {
				managed = "yes"
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", item.Name, item.Actual, item.Desired, managed, item.Path)
		}
		writer.Flush()
	}
}

func listFilter(state, scope, source string, drift bool) (inventory.Filter, error) {
	filter := inventory.Filter{Drift: drift, Source: source}
	if state != "" {
		filter.State = model.InvocationState(state)
		if !filter.State.Valid() {
			return inventory.Filter{}, fmt.Errorf("invalid state %q", state)
		}
	}
	if scope != "" {
		filter.Scope = model.Scope(scope)
		switch filter.Scope {
		case model.ScopeSystem, model.ScopeUser, model.ScopePlugin, model.ScopeRepo, model.ScopeAdmin, model.ScopeOther:
		default:
			return inventory.Filter{}, fmt.Errorf("invalid scope %q", scope)
		}
	}
	return filter, nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return &ExitError{Code: 2, Err: fmt.Errorf("%s accepts no arguments", cmd.CommandPath())}
	}
	return nil
}

func exactArgs(count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != count {
			return &ExitError{Code: 2, Err: fmt.Errorf("%s requires exactly %d argument(s)", cmd.CommandPath(), count)}
		}
		return nil
	}
}
