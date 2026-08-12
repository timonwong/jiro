package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
	"golang.org/x/term"
)

type loginTerminal interface {
	IsTerminal() bool
	Prompt(label, defaultValue string) (string, error)
	PromptSecret(label string) (string, error)
}

type streamLoginTerminal struct {
	input  io.Reader
	output io.Writer
	reader *bufio.Reader
	file   *os.File
}

func newLoginTerminal(input io.Reader, output io.Writer) loginTerminal {
	terminal := &streamLoginTerminal{input: input, output: output, reader: bufio.NewReader(input)}
	if file, ok := input.(*os.File); ok && isTerminalFile(file) {
		terminal.file = file
	}
	return terminal
}

func isTerminalFile(file *os.File) bool {
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func (t *streamLoginTerminal) IsTerminal() bool { return t.file != nil }

func (t *streamLoginTerminal) Prompt(label, defaultValue string) (string, error) {
	if defaultValue == "" {
		_, _ = fmt.Fprintf(t.output, "%s: ", label)
	} else {
		_, _ = fmt.Fprintf(t.output, "%s [%s]: ", label, defaultValue)
	}
	value, err := t.reader.ReadString('\n')
	if err != nil && !(err == io.EOF && value != "") {
		return "", apperr.Wrap(apperr.KindInvalidInput, err, "read %s", strings.ToLower(label))
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultValue
	}
	return value, nil
}

func (t *streamLoginTerminal) PromptSecret(label string) (string, error) {
	if t.file == nil {
		return "", apperr.New(apperr.KindInvalidInput, "credential prompt requires a terminal")
	}
	_, _ = fmt.Fprintf(t.output, "%s: ", label)
	secret, err := term.ReadPassword(int(t.file.Fd()))
	_, _ = fmt.Fprintln(t.output)
	if err != nil {
		return "", apperr.Wrap(apperr.KindInvalidInput, err, "read credential")
	}
	return string(secret), nil
}

type loginResult struct {
	Profile         string          `json:"profile"`
	Host            string          `json:"host"`
	AuthType        config.AuthType `json:"authType"`
	CredentialStore string          `json:"credentialStore"`
	User            jira.User       `json:"user"`
}

type logoutResult struct {
	Profile                     string `json:"profile"`
	CredentialStore             string `json:"credentialStore"`
	CredentialRemoved           bool   `json:"credentialRemoved"`
	EnvironmentCredentialActive bool   `json:"environmentCredentialActive"`
}

type authStatusResult struct {
	Profile  string          `json:"profile"`
	Instance string          `json:"instance"`
	AuthType config.AuthType `json:"authType"`
	User     jira.User       `json:"user"`
}

func (a *app) authCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage Jira authentication"}
	command.AddCommand(
		a.authLoginCommand(),
		a.authLogoutCommand(),
		a.authStatusCommand(),
	)
	return command
}

func (a *app) authLoginCommand() *cobra.Command {
	useKeyring := true
	passwordStdin := false
	tokenStdin := false
	command := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and save a Jira Profile",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if passwordStdin && tokenStdin {
				return apperr.New(apperr.KindInvalidInput, "--password-stdin and --token-stdin are mutually exclusive")
			}
			draft, err := config.LoadDraft(a.configOptions())
			if err != nil {
				return err
			}
			candidate, err := a.loginCandidate(draft, useKeyring, passwordStdin, tokenStdin)
			if err != nil {
				return err
			}
			user, err := verifyLogin(command.Context(), candidate)
			if err != nil {
				return err
			}
			if err := config.CommitLogin(draft, candidate, a.secretStore); err != nil {
				return err
			}
			store := credentialStore(candidate.UseKeyring)
			result := loginResult{
				Profile: profileName(candidate.Profile), Host: candidate.Host,
				AuthType: candidate.AuthType, CredentialStore: store, User: user,
			}
			return a.render(result, output.Table{
				Columns: []output.Column{output.Fixed("FIELD"), output.Flexible("VALUE")},
				Rows: [][]string{
					{"Profile", result.Profile}, {"Host", result.Host},
					{"Auth Type", string(result.AuthType)}, {"Credential Store", result.CredentialStore},
					{"User", displayUser(result.User)},
				},
			})
		},
	}
	command.Flags().BoolVar(&useKeyring, "use-keyring", true, "store the credential in the OS keyring; false stores it in the 0600 config file")
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read a Basic Auth password from stdin; requires JIRA_USERNAME")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read a Personal Access Token from stdin")
	return command
}

func (a *app) authLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove a Jira Profile credential",
		Args:  exactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			removed, err := config.Logout(a.configOptions(), a.secretStore)
			if err != nil {
				return err
			}
			selectedProfile := a.profile
			if selectedProfile == "" {
				selectedProfile = os.Getenv("JIRO_PROFILE")
			}
			result := logoutResult{
				Profile: profileName(selectedProfile), CredentialStore: removed.CredentialStore,
				CredentialRemoved:           removed.CredentialRemoved,
				EnvironmentCredentialActive: os.Getenv("JIRA_PASSWORD") != "" || os.Getenv("JIRA_TOKEN") != "",
			}
			return a.render(result, output.Table{
				Columns: []output.Column{output.Fixed("FIELD"), output.Flexible("VALUE")},
				Rows: [][]string{
					{"Profile", result.Profile}, {"Credential Store", result.CredentialStore},
					{"Credential Removed", fmt.Sprint(result.CredentialRemoved)},
					{"Environment Credential Active", fmt.Sprint(result.EnvironmentCredentialActive)},
				},
			})
		},
	}
}

func (a *app) authStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Verify the current Jira authentication",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			user, err := client.Myself(command.Context())
			if err != nil {
				return err
			}
			result := authStatusResult{
				Profile: profileName(settings.Profile), Instance: settings.Host,
				AuthType: settings.AuthType, User: user,
			}
			return a.render(result, output.Table{
				Columns: []output.Column{output.Fixed("FIELD"), output.Flexible("VALUE")},
				Rows: [][]string{
					{"Profile", result.Profile}, {"Jira Instance", result.Instance},
					{"Auth Type", string(result.AuthType)}, {"User", displayUser(result.User)},
					{"Active", strconv.FormatBool(result.User.Active)},
				},
			})
		},
	}
}

func (a *app) loginCandidate(draft config.Draft, useKeyring, passwordStdin, tokenStdin bool) (config.Settings, error) {
	candidate := draft.Settings
	// Login credentials are always fresh. In particular, never reuse a secret
	// from an existing Profile while constructing or verifying the candidate.
	candidate.Password = ""
	candidate.Token = ""
	candidate.UseKeyring = useKeyring
	if candidate.APIVersion == 0 {
		candidate.APIVersion = 2
	}
	if strings.TrimSpace(candidate.UserAgent) == "" {
		candidate.UserAgent = defaultUserAgent()
	}

	if a.terminal.IsTerminal() {
		host, err := a.terminal.Prompt("Host", candidate.Host)
		if err != nil {
			return config.Settings{}, err
		}
		candidate.Host = host
	}

	if passwordStdin || tokenStdin {
		return a.stdinLoginCandidate(candidate, passwordStdin)
	}

	environmentCredential, environmentActive, err := config.LoadEnvironmentCredential()
	if err != nil {
		return config.Settings{}, err
	}
	if environmentActive {
		environmentCredential.ApplyTo(&candidate)
		if err := normalizeLoginFields(&candidate); err != nil {
			return config.Settings{}, err
		}
		return candidate, nil
	}

	if !a.terminal.IsTerminal() {
		return config.Settings{}, apperr.New(apperr.KindInvalidInput, "non-TTY login requires a complete JIRA_TOKEN or JIRA_USERNAME/JIRA_PASSWORD credential, or --password-stdin/--token-stdin")
	}

	if !draft.Exists {
		candidate.AuthType = ""
	}
	authDefault := string(candidate.AuthType)
	if !draft.Exists {
		authDefault = ""
	}
	auth, err := a.terminal.Prompt("Auth type (basic/pat)", authDefault)
	if err != nil {
		return config.Settings{}, err
	}
	candidate.AuthType = config.AuthType(strings.ToLower(strings.TrimSpace(auth)))
	if candidate.AuthType == config.AuthBasic {
		candidate.Username, err = a.terminal.Prompt("Username", candidate.Username)
		if err != nil {
			return config.Settings{}, err
		}
	} else {
		candidate.Username = ""
	}
	if err := normalizeLoginFields(&candidate); err != nil {
		return config.Settings{}, err
	}
	secret, err := a.terminal.PromptSecret(secretLabel(candidate.AuthType))
	if err != nil {
		return config.Settings{}, err
	}
	if secret == "" {
		return config.Settings{}, apperr.New(apperr.KindInvalidInput, strings.ToLower(secretLabel(candidate.AuthType))+" must not be empty")
	}
	setLoginSecret(&candidate, secret)
	return candidate, nil
}

func (a *app) stdinLoginCandidate(candidate config.Settings, passwordStdin bool) (config.Settings, error) {
	if os.Getenv("JIRA_TOKEN") != "" || os.Getenv("JIRA_PASSWORD") != "" {
		return config.Settings{}, apperr.New(apperr.KindInvalidInput, "stdin credential mode conflicts with non-empty JIRA_TOKEN or JIRA_PASSWORD")
	}
	if passwordStdin {
		candidate.AuthType = config.AuthBasic
		candidate.Username = os.Getenv("JIRA_USERNAME")
	} else {
		candidate.AuthType = config.AuthPAT
		candidate.Username = ""
	}
	if err := normalizeLoginFields(&candidate); err != nil {
		return config.Settings{}, err
	}
	secret, err := a.readStdinCredential()
	if err != nil {
		return config.Settings{}, err
	}
	setLoginSecret(&candidate, secret)
	return candidate, nil
}

// readStdinCredential restores the default signal disposition around the read,
// because a credential read that blocks on a terminal cannot observe
// cancellation any more than an interactive prompt can.
func (a *app) readStdinCredential() (string, error) {
	if a.signals != nil {
		resume := a.signals.suspend()
		defer resume()
	}
	return readLoginCredential(a.stdin)
}

func readLoginCredential(input io.Reader) (string, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return "", apperr.Wrap(apperr.KindInvalidInput, err, "read credential from stdin")
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	if len(data) == 0 {
		return "", apperr.New(apperr.KindInvalidInput, "credential read from stdin must not be empty")
	}
	return string(data), nil
}

func setLoginSecret(candidate *config.Settings, secret string) {
	if candidate.AuthType == config.AuthPAT {
		candidate.Token = secret
		candidate.Password = ""
		return
	}
	candidate.Password = secret
	candidate.Token = ""
}

func normalizeLoginFields(candidate *config.Settings) error {
	if candidate.APIVersion != 2 {
		return apperr.New(apperr.KindConfig, "jiro v1 supports Jira REST API version 2 only")
	}
	if strings.TrimSpace(candidate.Host) == "" {
		return apperr.New(apperr.KindInvalidInput, "host is required")
	}
	host, err := config.NormalizeHost(candidate.Host)
	if err != nil {
		return apperr.New(apperr.KindInvalidInput, apperr.As(err).Error())
	}
	candidate.Host = host
	if candidate.AuthType != config.AuthBasic && candidate.AuthType != config.AuthPAT {
		return apperr.New(apperr.KindInvalidInput, "auth type must be basic or pat")
	}
	if candidate.AuthType == config.AuthBasic && strings.TrimSpace(candidate.Username) == "" {
		return apperr.New(apperr.KindInvalidInput, "username is required for basic authentication")
	}
	return nil
}

func verifyLogin(ctx context.Context, candidate config.Settings) (jira.User, error) {
	client, err := clientFromSettings(candidate)
	if err != nil {
		return jira.User{}, err
	}
	return client.Myself(ctx)
}

func secretLabel(auth config.AuthType) string {
	if auth == config.AuthPAT {
		return "Token"
	}
	return "Password"
}

func credentialStore(useKeyring bool) string {
	if useKeyring {
		return "keyring"
	}
	return "config"
}

func profileName(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return "default"
	}
	return profile
}

func displayUser(user jira.User) string {
	if user.DisplayName != "" && user.Username != "" && user.DisplayName != user.Username {
		return fmt.Sprintf("%s (%s)", user.DisplayName, user.Username)
	}
	if user.DisplayName != "" {
		return user.DisplayName
	}
	if user.Username != "" {
		return user.Username
	}
	return user.AccountID
}
