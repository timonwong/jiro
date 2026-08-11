// Package fieldcache persists Jira field definitions scoped to an instance and
// the authenticated principal that can see them.
package fieldcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

const (
	schemaVersion = 3
	cacheTTL      = 24 * time.Hour
)

var (
	// ErrNotFound reports that no snapshot has been saved for the requested
	// instance and principal.
	ErrNotFound = errors.New("field cache snapshot not found")
	// ErrInvalid reports a corrupt or mismatched cached snapshot.
	ErrInvalid = errors.New("invalid field cache snapshot")
)

// Principal is the identity returned by Jira's /myself endpoint. AccountID is
// the stable canonical identity when Jira provides it; Username is the legacy
// fallback used by Jira Server instances.
type Principal struct {
	AccountID string `json:"accountId,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Field is the cache-safe subset of a Jira field definition.
type Field struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Alias        string `json:"alias,omitempty"`
	Custom       bool   `json:"custom"`
	Type         string `json:"type,omitempty"`
	SchemaCustom string `json:"schemaCustom,omitempty"`
}

// Snapshot is a versioned on-disk field-cache record.
type Snapshot struct {
	SchemaVersion int       `json:"schemaVersion"`
	Instance      string    `json:"instance"`
	Principal     Principal `json:"principal"`
	FetchedAt     time.Time `json:"fetchedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Fields        []Field   `json:"fields"`
}

// Store owns field-cache storage. Leave Root empty to use xdg.CacheHome/jiro.
// Leave Now nil to use time.Now. Root and Now are
// exported for test injection and for callers that need an isolated cache.
type Store struct {
	Root string
	Now  func() time.Time
}

// New returns a Store with the supplied optional root and clock. Empty root
// uses xdg.CacheHome/jiro and nil now uses time.Now.
func New(root string, now func() time.Time) Store {
	return Store{Root: root, Now: now}
}

// Path returns the location assigned to instance and principal. Instance must
// be a normalized, absolute Jira base URL. A principal requires accountId or
// username.
func (s Store) Path(instance string, principal Principal) (string, error) {
	if err := validateInstance(instance); err != nil {
		return "", err
	}
	principalKey, err := principal.canonicalKey()
	if err != nil {
		return "", err
	}
	root, err := s.root()
	if err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s-%s--%s-%s.json",
		slug(instance, "instance"), shortHash(instance),
		slug(principal.displayKey(), "principal"), shortHash(principalKey),
	)
	return filepath.Join(root, "fields", "hosts", filename), nil
}

// NewSnapshot makes a fresh, 24-hour snapshot. It copies and sorts fields by
// ID, so callers may safely retain or reuse their input slice.
func (s Store) NewSnapshot(instance string, principal Principal, fields []Field) (Snapshot, error) {
	if _, err := s.Path(instance, principal); err != nil {
		return Snapshot{}, err
	}
	fetchedAt := s.now().UTC()
	copyFields := append([]Field(nil), fields...)
	sort.SliceStable(copyFields, func(i, j int) bool { return copyFields[i].ID < copyFields[j].ID })
	if copyFields == nil {
		copyFields = []Field{}
	}
	return Snapshot{
		SchemaVersion: schemaVersion,
		Instance:      instance,
		Principal:     principal,
		FetchedAt:     fetchedAt,
		ExpiresAt:     fetchedAt.Add(cacheTTL),
		Fields:        copyFields,
	}, nil
}

// Fresh reports whether the snapshot is still usable at the Store's clock.
// A snapshot exactly 24 hours old is stale.
func (s Store) Fresh(snapshot Snapshot) bool {
	return snapshot.ExpiresAt.After(s.now())
}

// Stale is the inverse of Fresh.
func (s Store) Stale(snapshot Snapshot) bool {
	return !s.Fresh(snapshot)
}

// Write atomically replaces the snapshot for its instance and principal. It
// returns the actual path used for warnings and diagnostics.
func (s Store) Write(snapshot Snapshot) (string, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return "", err
	}
	path, err := s.Path(snapshot.Instance, snapshot.Principal)
	if err != nil {
		return "", err
	}
	snapshot.Fields = append([]Field(nil), snapshot.Fields...)
	sort.SliceStable(snapshot.Fields, func(i, j int) bool { return snapshot.Fields[i].ID < snapshot.Fields[j].ID })
	if snapshot.Fields == nil {
		snapshot.Fields = []Field{}
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal field cache snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := s.ensureCacheDir(); err != nil {
		return "", err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".fieldcache-*")
	if err != nil {
		return "", fmt.Errorf("create field cache temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("set field cache temporary permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write field cache temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync field cache temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close field cache temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("replace field cache snapshot: %w", err)
	}
	return path, nil
}

// Read returns a validated snapshot and its actual cache path. Missing files
// wrap ErrNotFound; malformed, stale-format, or identity-mismatched files wrap
// ErrInvalid.
func (s Store) Read(instance string, principal Principal) (Snapshot, string, error) {
	path, err := s.Path(instance, principal)
	if err != nil {
		return Snapshot{}, "", err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, path, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return Snapshot{}, path, fmt.Errorf("read field cache snapshot %q: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, path, invalid(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Snapshot{}, path, invalid(errors.New("multiple JSON values"))
		}
		return Snapshot{}, path, invalid(err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, path, err
	}
	if snapshot.Instance != instance {
		return Snapshot{}, path, invalid(errors.New("instance does not match requested cache key"))
	}
	storedKey, _ := snapshot.Principal.canonicalKey()
	wantKey, _ := principal.canonicalKey()
	if storedKey != wantKey {
		return Snapshot{}, path, invalid(errors.New("principal does not match requested cache key"))
	}
	return snapshot, path, nil
}

func (s Store) root() (string, error) {
	if s.Root != "" {
		return s.Root, nil
	}
	return filepath.Join(xdg.CacheHome, "jiro"), nil
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (p Principal) canonicalKey() (string, error) {
	if accountID := strings.TrimSpace(p.AccountID); accountID != "" {
		return "accountId:" + accountID, nil
	}
	if username := strings.TrimSpace(p.Username); username != "" {
		return "username:" + username, nil
	}
	return "", fmt.Errorf("%w: principal requires accountId or username", ErrInvalid)
}

func (p Principal) displayKey() string {
	if accountID := strings.TrimSpace(p.AccountID); accountID != "" {
		return accountID
	}
	return strings.TrimSpace(p.Username)
}

func validateInstance(instance string) error {
	parsed, err := url.Parse(instance)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: instance must be a normalized absolute base URL", ErrInvalid)
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.SchemaVersion != schemaVersion {
		return invalid(fmt.Errorf("unsupported schema version %d", snapshot.SchemaVersion))
	}
	if err := validateInstance(snapshot.Instance); err != nil {
		return err
	}
	if _, err := snapshot.Principal.canonicalKey(); err != nil {
		return err
	}
	if snapshot.FetchedAt.IsZero() || snapshot.ExpiresAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.FetchedAt) {
		return invalid(errors.New("invalid snapshot timestamps"))
	}
	if !snapshot.ExpiresAt.Equal(snapshot.FetchedAt.Add(cacheTTL)) {
		return invalid(errors.New("snapshot expiry does not match cache TTL"))
	}
	for _, field := range snapshot.Fields {
		if strings.TrimSpace(field.ID) == "" {
			return invalid(errors.New("field ID must not be empty"))
		}
	}
	return nil
}

func (s Store) ensureCacheDir() error {
	root, err := s.root()
	if err != nil {
		return err
	}
	dirs := []string{root, filepath.Join(root, "fields"), filepath.Join(root, "fields", "hosts")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create field cache directory %q: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, 0o700); err != nil {
				return fmt.Errorf("set field cache directory permissions %q: %w", dir, err)
			}
		}
	}
	return nil
}

func slug(value, fallback string) string {
	const maxLength = 64
	var builder strings.Builder
	separator := false
	for _, char := range strings.ToLower(value) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(char)
			separator = false
		} else {
			separator = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		result = fallback
	}
	if len(result) > maxLength {
		result = strings.TrimRight(result[:maxLength], "-")
	}
	return result
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func invalid(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}
