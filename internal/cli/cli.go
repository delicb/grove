package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"github.com/del-boy/grove/internal/app"
	"github.com/del-boy/grove/internal/bootstrap"
	"github.com/del-boy/grove/internal/config"
	gitadapter "github.com/del-boy/grove/internal/git"
	"github.com/del-boy/grove/internal/model"
	"github.com/del-boy/grove/internal/output"
)

const defaultVersion = "dev"

type Options struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Version    string
	IsTerminal func() bool
}

type commandTree struct {
	ConfigFile  *string          `name:"config" help:"Use this configuration file." placeholder:"path"`
	VersionFlag kong.VersionFlag `name:"version" help:"Show the Grove version."`
	Create      createCommand    `cmd:"" help:"Create a managed worktree."`
	List        listCommand      `cmd:"" help:"List managed worktrees."`
	Touch       touchCommand     `cmd:"" help:"Update Grove activity for a managed worktree."`
	Stats       statsCommand     `cmd:"" help:"Show managed worktree statistics."`
	Cleanup     cleanupCommand   `cmd:"" help:"Remove old clean worktrees. Run grove touch when you resume work."`
	Config      configCommand    `cmd:"" help:"Inspect the effective configuration."`
	Version     versionCommand   `cmd:"" help:"Show the Grove version."`
}

type createCommand struct {
	Name            string  `arg:"" name:"name" help:"Worktree name."`
	Repository      *string `name:"repo" help:"Repository path." placeholder:"path"`
	Branch          *string `name:"branch" help:"Branch name. The default is the worktree name." placeholder:"name"`
	Base            *string `name:"base" help:"Base commit for a new branch. The default is the selected HEAD." placeholder:"ref"`
	UseExisting     bool    `name:"use-existing" help:"Attach an existing branch."`
	Agent           *string `name:"agent" help:"Creator agent identity." placeholder:"id"`
	BootstrapScript *string `name:"bootstrap-script" help:"Use this bootstrap script." placeholder:"path"`
	NoBootstrap     bool    `name:"no-bootstrap" help:"Disable bootstrap execution."`
	JSON            bool    `name:"json" help:"Write JSON output."`
}

type listCommand struct {
	Repository *string `name:"repo" help:"Filter by repository." placeholder:"path"`
	All        bool    `name:"all" help:"Include all worktree states."`
	Refresh    bool    `name:"refresh-size" help:"Measure active worktree sizes before output."`
	JSON       bool    `name:"json" help:"Write JSON output."`
}

type touchCommand struct {
	Target     string  `arg:"" name:"name-or-absolute-path" help:"Worktree name or absolute path."`
	Repository *string `name:"repo" help:"Repository path for a worktree name." placeholder:"path"`
	JSON       bool    `name:"json" help:"Write JSON output."`
}

type statsCommand struct {
	Repository *string `name:"repo" help:"Filter by repository." placeholder:"path"`
	All        bool    `name:"all" help:"Include final-state counts."`
	Refresh    bool    `name:"refresh" help:"Measure active worktree sizes before output."`
	JSON       bool    `name:"json" help:"Write JSON output."`
}

type cleanupCommand struct {
	Repository   *string `name:"repo" help:"Filter by repository." placeholder:"path"`
	OlderThan    string  `name:"older-than" required:"" help:"Use this positive inactivity age." placeholder:"age"`
	AllowIgnored bool    `name:"allow-ignored" help:"Permit deletion when ignored files are present."`
	DryRun       bool    `name:"dry-run" help:"Show decisions without deletion."`
	Yes          bool    `name:"yes" help:"Approve deletion without a prompt."`
	JSON         bool    `name:"json" help:"Write JSON output."`
}

type configCommand struct {
	Show configShowCommand `cmd:"" help:"Show effective values and their sources."`
	Path configPathCommand `cmd:"" help:"Show the selected configuration file path."`
}

type configShowCommand struct {
	JSON bool `name:"json" help:"Write JSON output."`
}

type configPathCommand struct {
	JSON bool `name:"json" help:"Write JSON output."`
}

type versionCommand struct{}

type runner struct {
	options Options
	tree    commandTree
}

type kongExit int

func Run(ctx context.Context, args []string, options Options) (exitCode int) {
	options = withOptionDefaults(options)
	defer func() {
		value := recover()
		if value == nil {
			return
		}
		if code, ok := value.(kongExit); ok {
			exitCode = int(code)
			return
		}
		panic(value)
	}()

	application := &runner{options: options}
	parser, err := kong.New(
		&application.tree,
		kong.Name("grove"),
		kong.Description("Manage local Git worktrees. Run grove touch when you resume work."),
		kong.Writers(options.Stdout, options.Stderr),
		kong.Exit(func(code int) { panic(kongExit(code)) }),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
		kong.Vars{"version": "grove " + options.Version},
	)
	if err != nil {
		return application.writeError(commandFromArgs(args), jsonRequested(args), output.DomainError(err))
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		domainErr := model.NewError(model.ErrorInvalidArguments, model.ExitInvalidArguments, err.Error(), err)
		return application.writeError(commandFromArgs(args), jsonRequested(args), domainErr)
	}
	return application.run(ctx, parsed.Command())
}

func withOptionDefaults(options Options) Options {
	if options.Stdin == nil {
		options.Stdin = os.Stdin
	}
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	if options.Version == "" {
		options.Version = defaultVersion
	}
	if options.IsTerminal == nil {
		options.IsTerminal = func() bool {
			input, inputOK := options.Stdin.(*os.File)
			standardOutput, standardOutputOK := options.Stdout.(*os.File)
			errorOutput, errorOutputOK := options.Stderr.(*os.File)
			return inputOK && standardOutputOK && errorOutputOK &&
				term.IsTerminal(int(input.Fd())) &&
				term.IsTerminal(int(standardOutput.Fd())) &&
				term.IsTerminal(int(errorOutput.Fd()))
		}
	}
	return options
}

func (runner *runner) run(ctx context.Context, command string) int {
	switch command {
	case "create <name>":
		return runner.runCreate(ctx)
	case "list":
		return runner.runList(ctx)
	case "touch <name-or-absolute-path>":
		return runner.runTouch(ctx)
	case "stats":
		return runner.runStats(ctx)
	case "cleanup":
		return runner.runCleanup(ctx)
	case "config show":
		return runner.runConfigShow()
	case "config path":
		return runner.runConfigPath()
	case "version":
		_, err := fmt.Fprintf(runner.options.Stdout, "grove %s\n", runner.options.Version)
		if err != nil {
			return model.ExitInternal
		}
		return model.ExitSuccess
	default:
		err := model.NewError(model.ErrorInvalidArguments, model.ExitInvalidArguments, "A command is required.", nil)
		return runner.writeError(command, false, err)
	}
}

func (runner *runner) runCreate(ctx context.Context) int {
	command := runner.tree.Create
	if err := validateOptionalValues(command.Repository); err != nil {
		return runner.writeError("create", command.JSON, err)
	}
	if command.Branch != nil && *command.Branch == "" {
		err := model.NewError(model.ErrorInvalidBranch, model.ExitInvalidArguments, "The branch name must not be empty.", nil)
		return runner.writeError("create", command.JSON, err)
	}
	if command.Base != nil && *command.Base == "" {
		err := model.NewError(model.ErrorInvalidBase, model.ExitGit, "The base reference must not be empty.", nil)
		return runner.writeError("create", command.JSON, err)
	}
	if command.Agent != nil && strings.TrimSpace(*command.Agent) == "" {
		err := model.NewError(model.ErrorInvalidAgent, model.ExitInvalidArguments, "The agent ID must not be empty.", nil)
		return runner.writeError("create", command.JSON, err)
	}
	service, err := runner.openService(ctx, command.JSON, command.BootstrapScript, command.NoBootstrap)
	if err != nil {
		return runner.writeError("create", command.JSON, err)
	}
	result, commandErr := service.Create(ctx, app.CreateOptions{
		Name:        command.Name,
		Repository:  optionalValue(command.Repository),
		Branch:      optionalValue(command.Branch),
		Base:        optionalValue(command.Base),
		UseExisting: command.UseExisting,
		Agent:       optionalValue(command.Agent),
	})
	commandErr = closeService(service, commandErr)
	if commandErr != nil {
		return runner.writeError("create", command.JSON, commandErr)
	}
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
	} else {
		if err := output.WriteCreate(runner.options.Stdout, result.Data); err != nil {
			return model.ExitInternal
		}
		if err := output.WriteIssues(runner.options.Stderr, result.Warnings, result.Failures); err != nil {
			return model.ExitInternal
		}
	}
	return output.ResultExitCode(result.Failures, &result.Data.Bootstrap)
}

func (runner *runner) runList(ctx context.Context) int {
	command := runner.tree.List
	if err := validateOptionalValues(command.Repository); err != nil {
		return runner.writeError("list", command.JSON, err)
	}
	service, err := runner.openService(ctx, command.JSON, nil, false)
	if err != nil {
		return runner.writeError("list", command.JSON, err)
	}
	result, commandErr := service.List(ctx, app.ListOptions{
		Repository:  optionalValue(command.Repository),
		All:         command.All,
		RefreshSize: command.Refresh,
	})
	commandErr = closeService(service, commandErr)
	if commandErr != nil {
		return runner.writeError("list", command.JSON, commandErr)
	}
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
	} else {
		if err := output.WriteList(runner.options.Stdout, result.Data); err != nil {
			return model.ExitInternal
		}
		if err := output.WriteIssues(runner.options.Stderr, result.Warnings, result.Failures); err != nil {
			return model.ExitInternal
		}
	}
	return output.ResultExitCode(result.Failures, nil)
}

func (runner *runner) runTouch(ctx context.Context) int {
	command := runner.tree.Touch
	if err := validateOptionalValues(command.Repository); err != nil {
		return runner.writeError("touch", command.JSON, err)
	}
	service, err := runner.openService(ctx, command.JSON, nil, false)
	if err != nil {
		return runner.writeError("touch", command.JSON, err)
	}
	result, commandErr := service.Touch(ctx, app.TouchOptions{
		Target:     command.Target,
		Repository: optionalValue(command.Repository),
	})
	commandErr = closeService(service, commandErr)
	if commandErr != nil {
		return runner.writeError("touch", command.JSON, commandErr)
	}
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
	} else {
		if err := output.WriteTouch(runner.options.Stdout, result.Data); err != nil {
			return model.ExitInternal
		}
		if err := output.WriteIssues(runner.options.Stderr, result.Warnings, result.Failures); err != nil {
			return model.ExitInternal
		}
	}
	return output.ResultExitCode(result.Failures, nil)
}

func (runner *runner) runStats(ctx context.Context) int {
	command := runner.tree.Stats
	if err := validateOptionalValues(command.Repository); err != nil {
		return runner.writeError("stats", command.JSON, err)
	}
	service, err := runner.openService(ctx, command.JSON, nil, false)
	if err != nil {
		return runner.writeError("stats", command.JSON, err)
	}
	result, commandErr := service.Stats(ctx, app.StatsOptions{
		Repository: optionalValue(command.Repository),
		All:        command.All,
		Refresh:    command.Refresh,
	})
	commandErr = closeService(service, commandErr)
	if commandErr != nil {
		return runner.writeError("stats", command.JSON, commandErr)
	}
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
	} else {
		if err := output.WriteStats(runner.options.Stdout, result.Data); err != nil {
			return model.ExitInternal
		}
		if err := output.WriteIssues(runner.options.Stderr, result.Warnings, result.Failures); err != nil {
			return model.ExitInternal
		}
	}
	return output.ResultExitCode(result.Failures, nil)
}

func (runner *runner) runCleanup(ctx context.Context) int {
	command := runner.tree.Cleanup
	if err := validateOptionalValues(command.Repository); err != nil {
		return runner.writeError("cleanup", command.JSON, err)
	}
	if _, err := app.ParseAge(command.OlderThan); err != nil {
		return runner.writeError("cleanup", command.JSON, err)
	}
	if command.JSON && !command.DryRun && !command.Yes {
		err := model.NewError(
			model.ErrorConfirmationRequired,
			model.ExitConflict,
			"JSON cleanup requires --dry-run or --yes.",
			nil,
		)
		return runner.writeError("cleanup", true, err)
	}
	service, err := runner.openService(ctx, command.JSON, nil, false)
	if err != nil {
		return runner.writeError("cleanup", command.JSON, err)
	}
	options := app.CleanupOptions{
		Repository:   optionalValue(command.Repository),
		OlderThan:    command.OlderThan,
		AllowIgnored: command.AllowIgnored,
		DryRun:       command.DryRun,
	}
	if command.JSON {
		options.Approved = command.Yes
		result, commandErr := service.Cleanup(ctx, options)
		commandErr = closeService(service, commandErr)
		if commandErr != nil {
			return runner.writeError("cleanup", true, commandErr)
		}
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
		return output.ResultExitCode(result.Failures, nil)
	}

	plan, commandErr := service.PlanCleanup(ctx, options)
	if commandErr != nil {
		commandErr = closeService(service, commandErr)
		return runner.writeError("cleanup", false, commandErr)
	}
	if err := output.WriteCleanup(runner.options.Stdout, plan.Data); err != nil {
		_ = service.Close()
		return model.ExitInternal
	}
	if err := output.WriteIssues(runner.options.Stderr, plan.Warnings, plan.Failures); err != nil {
		_ = service.Close()
		return model.ExitInternal
	}
	if command.DryRun || plan.Data.Summary.Candidate == 0 {
		commandErr = closeService(service, nil)
		if commandErr != nil {
			return runner.writeError("cleanup", false, commandErr)
		}
		return output.ResultExitCode(plan.Failures, nil)
	}
	if !command.Yes {
		if !runner.options.IsTerminal() {
			_ = service.Close()
			err := model.NewError(
				model.ErrorConfirmationRequired,
				model.ExitConflict,
				"Cleanup requires a terminal confirmation or --yes.",
				nil,
			)
			return runner.writeError("cleanup", false, err)
		}
		approved, err := runner.confirmCleanup(plan.Data.Summary.Candidate)
		if err != nil {
			_ = service.Close()
			return runner.writeError("cleanup", false, err)
		}
		if !approved {
			commandErr = closeService(service, nil)
			if commandErr != nil {
				return runner.writeError("cleanup", false, commandErr)
			}
			if _, err := fmt.Fprintln(runner.options.Stdout, "Cleanup canceled."); err != nil {
				return model.ExitInternal
			}
			return output.ResultExitCode(plan.Failures, nil)
		}
	}
	options.Approved = true
	result, commandErr := service.ExecuteCleanupPlan(ctx, options, plan.Data)
	commandErr = closeService(service, commandErr)
	if commandErr != nil {
		return runner.writeError("cleanup", false, commandErr)
	}
	if err := output.WriteCleanupSummary(runner.options.Stdout, result.Data.Summary); err != nil {
		return model.ExitInternal
	}
	if err := output.WriteIssues(runner.options.Stderr, result.Warnings, result.Failures); err != nil {
		return model.ExitInternal
	}
	return output.ResultExitCode(result.Failures, nil)
}

func (runner *runner) runConfigShow() int {
	command := runner.tree.Config.Show
	loaded, err := runner.loadConfig(nil, false)
	if err != nil {
		return runner.writeError("config show", command.JSON, err)
	}
	result := model.NewResult("config show", loaded.ShowData())
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
		return model.ExitSuccess
	}
	if err := output.WriteConfigShow(runner.options.Stdout, result.Data, loaded.DataDirSource); err != nil {
		return model.ExitInternal
	}
	return model.ExitSuccess
}

func (runner *runner) runConfigPath() int {
	command := runner.tree.Config.Path
	loaded, err := runner.loadConfig(nil, false)
	if err != nil {
		return runner.writeError("config path", command.JSON, err)
	}
	result := model.NewResult("config path", loaded.PathData())
	if command.JSON {
		if err := output.WriteJSON(runner.options.Stdout, result); err != nil {
			return model.ExitInternal
		}
		return model.ExitSuccess
	}
	if err := output.WriteConfigPath(runner.options.Stdout, result.Data); err != nil {
		return model.ExitInternal
	}
	return model.ExitSuccess
}

func (runner *runner) openService(ctx context.Context, jsonMode bool, bootstrapScript *string, noBootstrap bool) (*app.Service, error) {
	if runner.tree.ConfigFile != nil && *runner.tree.ConfigFile == "" {
		return nil, model.NewError(model.ErrorConfigInvalid, model.ExitConfiguration, "The configuration path must not be empty.", nil)
	}
	mode := bootstrap.PassthroughOutput
	if jsonMode {
		mode = bootstrap.CaptureOutput
	}
	return app.Open(ctx, app.OpenOptions{
		Config: config.Options{
			ConfigPath:      optionalValue(runner.tree.ConfigFile),
			BootstrapScript: bootstrapScript,
			NoBootstrap:     noBootstrap,
		},
		Git:             gitadapter.NewClient(),
		BootstrapMode:   mode,
		BootstrapStdin:  runner.options.Stdin,
		BootstrapStdout: runner.options.Stdout,
		BootstrapStderr: runner.options.Stderr,
	})
}

func (runner *runner) loadConfig(bootstrapScript *string, noBootstrap bool) (config.Config, error) {
	if runner.tree.ConfigFile != nil && *runner.tree.ConfigFile == "" {
		return config.Config{}, model.NewError(model.ErrorConfigInvalid, model.ExitConfiguration, "The configuration path must not be empty.", nil)
	}
	return config.Load(config.Options{
		ConfigPath:      optionalValue(runner.tree.ConfigFile),
		BootstrapScript: bootstrapScript,
		NoBootstrap:     noBootstrap,
	})
}

func (runner *runner) confirmCleanup(candidateCount int) (bool, error) {
	noun := "worktrees"
	if candidateCount == 1 {
		noun = "worktree"
	}
	if _, err := fmt.Fprintf(runner.options.Stderr, "Delete %d %s? [y/N] ", candidateCount, noun); err != nil {
		return false, model.NewError(model.ErrorInternal, model.ExitInternal, "Grove could not write the cleanup prompt.", err)
	}
	line, err := bufio.NewReader(runner.options.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, model.NewError(model.ErrorInternal, model.ExitInternal, "Grove could not read the cleanup confirmation.", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func (runner *runner) writeError(command string, jsonMode bool, err error) int {
	if jsonMode {
		if writeErr := output.WriteJSONError(runner.options.Stderr, command, err); writeErr != nil {
			return model.ExitInternal
		}
	} else if writeErr := output.WriteHumanError(runner.options.Stderr, err); writeErr != nil {
		return model.ExitInternal
	}
	return output.ExitCode(err)
}

func closeService(service *app.Service, commandErr error) error {
	closeErr := service.Close()
	if closeErr == nil {
		return commandErr
	}
	return model.NewError(model.ErrorDatabase, model.ExitDatabase, "Grove could not close the database.", errors.Join(commandErr, closeErr))
}

func validateOptionalValues(values ...*string) error {
	for _, value := range values {
		if value != nil && *value == "" {
			return model.NewError(model.ErrorInvalidPath, model.ExitInvalidArguments, "A path option must not be empty.", nil)
		}
	}
	return nil
}

func optionalValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func commandFromArgs(args []string) string {
	for index := 0; index < len(args); index++ {
		value := args[index]
		if value == "--config" {
			index++
			continue
		}
		if strings.HasPrefix(value, "--config=") || strings.HasPrefix(value, "-") {
			continue
		}
		switch value {
		case "create", "list", "touch", "stats", "cleanup", "version":
			return value
		case "config":
			if index+1 < len(args) && (args[index+1] == "show" || args[index+1] == "path") {
				return "config " + args[index+1]
			}
			return "config"
		default:
			return value
		}
	}
	return "grove"
}

func jsonRequested(args []string) bool {
	for _, value := range args {
		if value == "--json" {
			return true
		}
		if raw, found := strings.CutPrefix(value, "--json="); found {
			parsed, err := strconv.ParseBool(raw)
			if err == nil && parsed {
				return true
			}
		}
	}
	return false
}
