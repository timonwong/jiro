// Package cmd defines jiro's public Cobra command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/fieldcache"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

var version = "dev"

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	terminal    loginTerminal
	outputTTY   outputTerminalDetector
	secretStore config.SecretStore
	fieldStore  fieldcache.Store
	signals     *executionNotifier
	warnings    []output.Warning

	configPath string
	profile    string
	output     string
	quiet      bool
}

// Execute runs jiro and returns a stable process exit code.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	notifier := installExecutionSignals()
	defer notifier.stop()
	a.signals = notifier
	a.terminal = signalSuspendingTerminal{terminal: newLoginTerminal(stdin, stderr), notifier: notifier}
	return a.executeContext(notifier.ctx, args, notifier.exitCode)
}

func (a *app) execute(args []string) int {
	return a.executeContext(context.Background(), args, func() int { return 0 })
}

func (a *app) executeContext(ctx context.Context, args []string, signalCode func() int) int {
	return a.executeWithRoot(ctx, a.rootCommand(), args, signalCode)
}

func (a *app) executeWithRoot(ctx context.Context, root *cobra.Command, args []string, signalCode func() int) int {
	a.warnings = nil
	if a.terminal == nil {
		a.terminal = newLoginTerminal(a.stdin, a.stderr)
	}
	root.SetContext(ctx)
	root.SetArgs(args)
	executed, err := root.ExecuteC()
	if err != nil {
		if code := signalCode(); code != 0 && errors.Is(ctx.Err(), context.Canceled) {
			_, _ = fmt.Fprintln(a.stderr, cancellationMessage(executed))
			return code
		}
		if isRawHTTPCommand(executed) {
			var rendered *apiRenderedError
			if !errors.As(err, &rendered) {
				_, _ = fmt.Fprintln(a.stderr, err)
			}
			return apperr.ExitCode(err)
		}
		renderer, renderErr := a.renderer()
		if renderErr != nil {
			err = renderErr
			renderer = output.New(a.stdout, a.stderr, output.FormatText, false)
		}
		_ = renderer.Error(err)
		return apperr.ExitCode(err)
	}
	return 0
}

// cancellationMessage names the work an interrupt ended, keeping the wording
// each command family already documents.
func cancellationMessage(command *cobra.Command) string {
	switch {
	case isJFMConversionCommand(command):
		return "conversion canceled"
	case isRawHTTPCommand(command):
		return "request canceled"
	default:
		return "canceled"
	}
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "jiro",
		Short:         "A scriptable Jira CLI for humans and AI agents",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if a.signals != nil && handlesBrokenPipe(command) {
				a.signals.ignoreBrokenPipe()
			}
			if command.Name() == "schema" {
				_, err := output.ParseFormat(a.output)
				return err
			}
			if isRawHTTPCommand(command) {
				if flag := command.Flags().Lookup("output"); flag != nil && flag.Changed {
					return apperr.New(apperr.KindInvalidInput, "--output is not supported for api")
				}
				return nil
			}
			_, err := a.renderer()
			return err
		},
	}
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return apperr.Wrap(apperr.KindInvalidInput, err, "invalid command flags")
	})

	flags := root.PersistentFlags()
	flags.StringVarP(&a.configPath, "config", "c", "", "config file path")
	flags.StringVar(&a.profile, "profile", "", "config profile name")
	flags.StringVarP(&a.output, "output", "o", "text", "output format: text or json")
	flags.BoolVar(&a.quiet, "quiet", false, "suppress successful text output")

	root.AddCommand(
		a.apiCommand(),
		a.authCommand(),
		a.boardCommand(),
		a.cacheCommand(),
		a.jfmCommand(),
		a.issueCommand(),
		a.projectCommand(),
		a.fieldCommand(),
		a.sprintCommand(),
		a.searchCommand(),
		a.schemaCommand(),
	)
	a.configureCompletions(root)
	return root
}

func (a *app) renderer() (output.Renderer, error) {
	format, err := output.ParseFormat(a.output)
	if err != nil {
		return output.Renderer{}, err
	}
	terminal := output.Terminal{}
	if format == output.FormatText {
		forceTTY, err := parseForceTTY()
		if err != nil {
			return output.Renderer{}, err
		}
		if a.outputTTY == nil {
			a.outputTTY = streamOutputTerminal{}
		}
		terminal = a.outputTTY.Detect(a.stdout, forceTTY)
	}
	return output.New(a.stdout, a.stderr, format, a.quiet).WithTerminal(terminal).WithWarnings(a.warnings...), nil
}

func (a *app) addWarning(warning output.Warning) {
	a.warnings = append(a.warnings, warning)
}

func (a *app) configOptions() config.Options {
	return config.Options{
		ConfigPath: a.configPath,
		Profile:    a.profile,
	}
}

func (a *app) settings() (config.Settings, error) {
	settings, err := config.Load(a.configOptions(), a.secretStore)
	if err != nil {
		return config.Settings{}, err
	}
	if settings.APIVersion != 2 {
		return config.Settings{}, apperr.New(apperr.KindConfig, "jiro v1 supports Jira REST API version 2 only")
	}
	if strings.TrimSpace(settings.UserAgent) == "" {
		settings.UserAgent = defaultUserAgent()
	}
	return settings, nil
}

func (a *app) client() (*jira.Client, config.Settings, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, config.Settings{}, err
	}
	client, err := clientFromSettings(settings)
	if err != nil {
		return nil, config.Settings{}, err
	}
	return client, settings, nil
}

// clientFromSettings builds a jira.Client from already-resolved settings. It
// is shared by a.client, the api command, and login verification, the last
// of which validates candidate settings before they are persisted and so
// cannot go through a.client itself.
func clientFromSettings(settings config.Settings) (*jira.Client, error) {
	clientConfig := jira.Config{BaseURL: settings.Host, Username: settings.Username, UserAgent: settings.UserAgent}
	if settings.AuthType == config.AuthPAT {
		clientConfig.PAT = settings.Token
	} else {
		clientConfig.Password = settings.Password
	}
	return jira.NewClient(clientConfig)
}

func defaultUserAgent() string {
	value := strings.TrimSpace(version)
	if value == "" {
		value = "dev"
	}
	return "jiro/" + value
}

func (a *app) writableClient() (*jira.Client, config.Settings, error) {
	preview, err := a.previewSettings()
	if err != nil {
		return nil, config.Settings{}, err
	}
	if err := a.requireWritable(preview); err != nil {
		return nil, config.Settings{}, err
	}
	return a.client()
}

// previewSettings resolves non-secret settings for validation that must
// complete before credential resolution (and any OS keyring access), shared
// by writableClient and the api command.
func (a *app) previewSettings() (config.Settings, error) {
	preview, err := config.LoadForDisplay(a.configOptions())
	if err != nil {
		return config.Settings{}, err
	}
	if preview.APIVersion != 2 {
		return config.Settings{}, apperr.New(apperr.KindConfig, "jiro v1 supports Jira REST API version 2 only")
	}
	return preview, nil
}

func (a *app) requireWritable(settings config.Settings) error {
	if settings.ReadOnly {
		return apperr.New(apperr.KindInvalidInput, "write operation blocked by read_only configuration")
	}
	return nil
}

func (a *app) render(data any, table output.Table) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	if renderer.Format == output.FormatText {
		return renderer.Success(table)
	}
	return renderer.Success(data)
}

func (a *app) renderMessage(data any, message string) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	if renderer.Format == output.FormatText {
		return renderer.Success(message)
	}
	return renderer.Success(data)
}

// renderPartial writes the complete result even when --quiet is set. Partial
// outcomes are failures, and their stdout data is part of the public contract.
func (a *app) renderPartial(data, text any) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	renderer.Quiet = false
	if renderer.Format == output.FormatText {
		return renderer.Success(text)
	}
	return renderer.Success(data)
}

func exactArgs(count int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != count {
			return apperr.New(apperr.KindInvalidInput, fmt.Sprintf("expected %d argument(s), got %d", count, len(args)))
		}
		return nil
	}
}

func validatePagination(offset, limit int) error {
	if offset < 0 {
		return apperr.New(apperr.KindInvalidInput, "offset must not be negative")
	}
	if limit < 0 {
		return apperr.New(apperr.KindInvalidInput, "limit must not be negative")
	}
	return nil
}

func joinNames(values []string) string {
	return strings.Join(values, ", ")
}
