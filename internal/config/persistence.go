package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/timonwong/jiro/internal/apperr"
)

const (
	credentialStoreKeyring = "keyring"
	credentialStoreConfig  = "config"
)

type configSource struct {
	path   string
	data   []byte
	exists bool
	info   os.FileInfo
}

var renameConfigFile = os.Rename

// LoadDraft reads a possibly incomplete default or named Profile for login.
// A missing named Profile inherits candidate defaults but is reported through
// Draft.Exists so callers can distinguish creation from update.
func LoadDraft(options Options) (Draft, error) {
	path, err := configPath(options)
	if err != nil {
		return Draft{}, err
	}
	source, file, err := loadConfigSource(path)
	if err != nil {
		return Draft{}, err
	}

	profile := firstNonEmpty(options.Profile, os.Getenv("JIRO_PROFILE"))
	persisted := file.Default
	exists := file.DefaultExists
	if profile != "" {
		selected, ok := file.Profiles[profile]
		exists = ok
		persisted = mergeProfile(file.Default, selected)
	}
	values := persisted
	environment, err := environmentValues()
	if err != nil {
		return Draft{}, err
	}
	values = merge(values, environment)
	values = merge(values, optionValues(options))
	settings, err := resolveDraft(profile, values)
	if err != nil {
		return Draft{}, err
	}
	if values.useKeyring == nil {
		settings.UseKeyring = true
	}

	return Draft{
		Settings:            settings,
		Exists:              exists,
		source:              source,
		persistedUseKeyring: exists && persisted.useKeyring != nil && *persisted.useKeyring,
	}, nil
}

// CommitLogin atomically persists a candidate that the command layer has
// already verified against Jira. The Draft source snapshot provides optimistic
// concurrency protection for the validation-to-commit window.
func CommitLogin(draft Draft, candidate Settings, store SecretStore) error {
	if draft.source.path == "" {
		return apperr.New(apperr.KindConfig, "login draft is not initialized")
	}
	if candidate.Profile != draft.Settings.Profile {
		return apperr.New(apperr.KindConfig, "login candidate profile does not match draft")
	}
	normalized, err := normalizeLoginCandidate(candidate)
	if err != nil {
		return err
	}
	secret := normalized.Secret()
	if secret == "" {
		return apperr.New(apperr.KindInvalidInput, credentialName(normalized.AuthType)+" must not be empty")
	}
	if err := draft.source.ensureUnchanged(); err != nil {
		return err
	}

	updated, err := updateProfileTOML(draft.source.data, normalized.Profile, loginTOMLUpdates(normalized, secret))
	if err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "update config %q", draft.source.path)
	}
	if _, err := parseTOML(string(updated)); err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "validate updated config %q", draft.source.path)
	}
	tomlChanged := !bytes.Equal(updated, draft.source.data)
	temporary := ""
	if tomlChanged {
		mode := os.FileMode(0o600)
		if draft.source.exists && normalized.UseKeyring {
			mode = draft.source.info.Mode().Perm()
		}
		temporary, err = prepareConfigTemp(draft.source.path, updated, mode)
		if err != nil {
			return err
		}
		defer os.Remove(temporary)
	}

	var credential *credentialChange
	if normalized.UseKeyring || draft.persistedUseKeyring {
		if store == nil {
			store = OSSecretStore{}
		}
		credential, err = beginCredentialChange(store, KeyringAccount(normalized.Profile))
		if err != nil {
			return err
		}
		if normalized.UseKeyring {
			err = credential.set(secret)
		} else {
			err = credential.delete()
		}
		if err != nil {
			return credential.fail(err)
		}
	}

	if err := draft.source.ensureUnchanged(); err != nil {
		if credential != nil {
			return credential.fail(err)
		}
		return err
	}
	if !tomlChanged {
		return nil
	}
	if err := renameConfigFile(temporary, draft.source.path); err != nil {
		wrapped := apperr.Wrap(apperr.KindConfig, err, "replace config %q", draft.source.path)
		if credential != nil {
			return credential.fail(wrapped)
		}
		return wrapped
	}
	return nil
}

// Logout removes only the selected Profile's persisted credential. Profile
// selection honors --profile/JIRO_PROFILE, while storage selection deliberately
// ignores all other environment and command-line overrides.
func Logout(options Options, store SecretStore) (LogoutResult, error) {
	path, err := configPath(options)
	if err != nil {
		return LogoutResult{}, err
	}
	source, file, err := loadConfigSource(path)
	if err != nil {
		return LogoutResult{}, err
	}
	profile := firstNonEmpty(options.Profile, os.Getenv("JIRO_PROFILE"))
	target := file.Default
	persisted := file.Default
	exists := file.DefaultExists
	if profile != "" {
		selected, ok := file.Profiles[profile]
		if !ok {
			return LogoutResult{}, apperr.New(apperr.KindConfig, fmt.Sprintf("profile %q not found", profile))
		}
		target = selected
		persisted = mergeProfile(file.Default, selected)
		exists = true
	}
	if !exists {
		return LogoutResult{}, apperr.New(apperr.KindConfig, "default profile not found")
	}

	useKeyring := persisted.useKeyring != nil && *persisted.useKeyring
	if useKeyring {
		result := LogoutResult{CredentialStore: credentialStoreKeyring}
		if store == nil {
			store = OSSecretStore{}
		}
		credential, err := beginCredentialChange(store, KeyringAccount(profile))
		if err != nil {
			return LogoutResult{}, err
		}
		if !credential.beforeExists {
			return result, source.ensureUnchanged()
		}
		if err := source.ensureUnchanged(); err != nil {
			return LogoutResult{}, err
		}
		if err := credential.delete(); err != nil {
			return LogoutResult{}, credential.fail(err)
		}
		if err := source.ensureUnchanged(); err != nil {
			return LogoutResult{}, credential.fail(err)
		}
		result.CredentialRemoved = true
		return result, nil
	}

	result := LogoutResult{
		CredentialStore:   credentialStoreConfig,
		CredentialRemoved: target.hasPlaintextSecret(),
	}
	updated, err := updateProfileTOML(source.data, profile, map[string]tomlUpdate{
		"password": {delete: true},
		"token":    {delete: true},
	})
	if err != nil {
		return LogoutResult{}, apperr.Wrap(apperr.KindConfig, err, "update config %q", path)
	}
	if bytes.Equal(updated, source.data) {
		return result, source.ensureUnchanged()
	}
	temporary, err := prepareConfigTemp(path, updated, source.info.Mode().Perm())
	if err != nil {
		return LogoutResult{}, err
	}
	defer os.Remove(temporary)
	if err := source.ensureUnchanged(); err != nil {
		return LogoutResult{}, err
	}
	if err := renameConfigFile(temporary, path); err != nil {
		return LogoutResult{}, apperr.Wrap(apperr.KindConfig, err, "replace config %q", path)
	}
	return result, nil
}

func loadConfigSource(path string) (configSource, fileConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return configSource{path: path}, fileConfig{Profiles: map[string]values{}}, nil
	}
	if err != nil {
		return configSource{}, fileConfig{}, apperr.Wrap(apperr.KindConfig, err, "read config %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return configSource{}, fileConfig{}, apperr.Wrap(apperr.KindConfig, err, "inspect config %q", path)
	}
	file, err := parseTOML(string(data))
	if err != nil {
		return configSource{}, fileConfig{}, apperr.Wrap(apperr.KindConfig, err, "parse config %q", path)
	}
	if runtime.GOOS != "windows" && file.hasPlaintextSecret() && info.Mode().Perm()&0o077 != 0 {
		return configSource{}, fileConfig{}, apperr.New(apperr.KindConfig, fmt.Sprintf("config %q contains a plaintext secret and must have permissions 0600", path))
	}
	return configSource{path: path, data: data, exists: true, info: info}, file, nil
}

func (source configSource) ensureUnchanged() error {
	data, err := os.ReadFile(source.path)
	if errors.Is(err, os.ErrNotExist) {
		if !source.exists {
			return nil
		}
		return concurrentConfigError(source.path)
	}
	if err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "re-read config %q", source.path)
	}
	if !source.exists {
		return concurrentConfigError(source.path)
	}
	info, err := os.Stat(source.path)
	if err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "re-inspect config %q", source.path)
	}
	if !bytes.Equal(data, source.data) || !os.SameFile(source.info, info) ||
		info.Mode().Perm() != source.info.Mode().Perm() || !info.ModTime().Equal(source.info.ModTime()) {
		return concurrentConfigError(source.path)
	}
	return nil
}

func concurrentConfigError(path string) error {
	return apperr.New(apperr.KindConfig, fmt.Sprintf("config %q changed concurrently; reload and retry", path))
}

func resolveDraft(profile string, values values) (Settings, error) {
	apiVersion := 2
	if values.apiVersion != nil {
		apiVersion = *values.apiVersion
	}
	if apiVersion <= 0 {
		return Settings{}, apperr.New(apperr.KindConfig, "api_version must be positive")
	}
	auth := AuthBasic
	if values.authType != nil {
		auth = AuthType(strings.ToLower(*values.authType))
	}
	if auth != AuthBasic && auth != AuthPAT {
		return Settings{}, apperr.New(apperr.KindConfig, "auth_type must be basic or pat")
	}
	settings := Settings{Profile: profile, AuthType: auth, APIVersion: apiVersion}
	if values.host != nil && strings.TrimSpace(*values.host) != "" {
		host, err := NormalizeHost(*values.host)
		if err != nil {
			return Settings{}, err
		}
		settings.Host = host
	}
	if values.username != nil {
		settings.Username = *values.username
	}
	if values.readOnly != nil {
		settings.ReadOnly = *values.readOnly
	}
	if values.useKeyring != nil {
		settings.UseKeyring = *values.useKeyring
	}
	if values.userAgent != nil {
		settings.UserAgent = *values.userAgent
	}
	if values.password != nil {
		settings.Password = *values.password
	}
	if values.token != nil {
		settings.Token = *values.token
	}
	return settings, nil
}

func normalizeLoginCandidate(candidate Settings) (Settings, error) {
	if candidate.AuthType != AuthBasic && candidate.AuthType != AuthPAT {
		return Settings{}, apperr.New(apperr.KindConfig, "auth_type must be basic or pat")
	}
	host, err := NormalizeHost(candidate.Host)
	if err != nil {
		return Settings{}, err
	}
	candidate.Host = host
	if candidate.AuthType == AuthBasic {
		candidate.Username = strings.TrimSpace(candidate.Username)
		if candidate.Username == "" {
			return Settings{}, apperr.New(apperr.KindInvalidInput, "username is required for basic authentication")
		}
		candidate.Token = ""
	} else {
		candidate.Username = ""
		candidate.Password = ""
	}
	return candidate, nil
}

func credentialName(auth AuthType) string {
	if auth == AuthPAT {
		return "token"
	}
	return "password"
}

func loginTOMLUpdates(candidate Settings, secret string) map[string]tomlUpdate {
	updates := map[string]tomlUpdate{
		"host":        {kind: tomlString, text: candidate.Host},
		"auth_type":   {kind: tomlString, text: string(candidate.AuthType)},
		"use_keyring": {kind: tomlBool, flag: candidate.UseKeyring},
	}
	if candidate.AuthType == AuthBasic {
		updates["username"] = tomlUpdate{kind: tomlString, text: candidate.Username}
		updates["token"] = tomlUpdate{delete: true}
		if candidate.UseKeyring {
			updates["password"] = tomlUpdate{delete: true}
		} else {
			updates["password"] = tomlUpdate{kind: tomlString, text: secret}
		}
		return updates
	}
	updates["username"] = tomlUpdate{delete: true}
	updates["password"] = tomlUpdate{delete: true}
	if candidate.UseKeyring {
		updates["token"] = tomlUpdate{delete: true}
	} else {
		updates["token"] = tomlUpdate{kind: tomlString, text: secret}
	}
	return updates
}

func prepareConfigTemp(path string, data []byte, mode os.FileMode) (string, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "create config directory %q", directory)
	}
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "create temporary config in %q", directory)
	}
	temporary := file.Name()
	ok := false
	defer func() {
		if !ok {
			file.Close()
			os.Remove(temporary)
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "set temporary config permissions")
	}
	if _, err := file.Write(data); err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "write temporary config")
	}
	if err := file.Sync(); err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "sync temporary config")
	}
	if err := file.Close(); err != nil {
		return "", apperr.Wrap(apperr.KindConfig, err, "close temporary config")
	}
	ok = true
	return temporary, nil
}

type credentialChange struct {
	store        SecretStore
	account      string
	before       string
	beforeExists bool
}

// ProbeSecretStore reads the Profile's keyring entry so login can choose a
// credential store before collecting a secret instead of failing at commit.
// A missing entry still proves the keyring is reachable.
func ProbeSecretStore(store SecretStore, profile string) error {
	if store == nil {
		store = OSSecretStore{}
	}
	_, err := store.Get(KeyringService, KeyringAccount(profile))
	if err == nil || errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	return err
}

func beginCredentialChange(store SecretStore, account string) (*credentialChange, error) {
	secret, err := store.Get(KeyringService, account)
	if errors.Is(err, ErrSecretNotFound) {
		return &credentialChange{store: store, account: account}, nil
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.KindConfig, err, "read credential from OS keyring")
	}
	return &credentialChange{store: store, account: account, before: secret, beforeExists: true}, nil
}

func (change *credentialChange) set(secret string) error {
	if err := change.store.Set(KeyringService, change.account, secret); err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "store credential in OS keyring")
	}
	return nil
}

func (change *credentialChange) delete() error {
	err := change.store.Delete(KeyringService, change.account)
	if errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	if err != nil {
		return apperr.Wrap(apperr.KindConfig, err, "delete credential from OS keyring")
	}
	return nil
}

func (change *credentialChange) restore() error {
	if change.beforeExists {
		return change.store.Set(KeyringService, change.account, change.before)
	}
	err := change.store.Delete(KeyringService, change.account)
	if errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	return err
}

func (change *credentialChange) fail(primary error) error {
	if rollback := change.restore(); rollback != nil {
		return apperr.Wrap(apperr.KindConfig, errors.Join(primary, rollback), "credential transaction failed and rollback was unsuccessful")
	}
	return primary
}
