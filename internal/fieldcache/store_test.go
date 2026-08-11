package fieldcache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
)

func TestPathDeterministicAndIsolated(t *testing.T) {
	t.Parallel()
	store := New("/cache", nil)
	principal := Principal{AccountID: "account-7", Username: "Alice Admin"}
	first, err := store.Path("https://jira.example.test:8443/jira", principal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Path("https://jira.example.test:8443/jira", principal)
	if err != nil || first != second {
		t.Fatalf("paths = %q, %q; err = %v", first, second, err)
	}
	if got, want := first, "/cache/fields/hosts/https-jira-example-test-8443-jira-e3d91461ccf96bf1--account-7-0aeae5cc7b9e1e02.json"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	renamed, err := store.Path("https://jira.example.test:8443/jira", Principal{AccountID: "account-7", Username: "Renamed User"})
	if err != nil || renamed != first {
		t.Fatalf("username rename path = %q, %v; want %q", renamed, err, first)
	}
	cases := []struct {
		name      string
		instance  string
		principal Principal
	}{
		{"scheme", "http://jira.example.test:8443/jira", principal},
		{"port", "https://jira.example.test:9443/jira", principal},
		{"path", "https://jira.example.test:8443/other", principal},
		{"principal", "https://jira.example.test:8443/jira", Principal{AccountID: "account-8", Username: "Alice Admin"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := store.Path(test.instance, test.principal)
			if err != nil {
				t.Fatal(err)
			}
			if got == first {
				t.Fatalf("%s shares path %q", test.name, got)
			}
		})
	}
	if _, err := store.Path("https://jira.example.test", Principal{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty principal error = %v", err)
	}
}

func TestDefaultRootUsesXDGCacheHome(t *testing.T) {
	original := xdg.CacheHome
	xdg.CacheHome = t.TempDir()
	t.Cleanup(func() { xdg.CacheHome = original })

	store := New("", nil)
	path, err := store.Path("https://jira.example.test", Principal{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(xdg.CacheHome, "jiro") + string(filepath.Separator)
	if !strings.HasPrefix(path, wantRoot) {
		t.Fatalf("Path() = %q, want root %q", path, wantRoot)
	}
}

func TestRoundTripSortAndFreshness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := New(t.TempDir(), func() time.Time { return now })
	principal := Principal{AccountID: "1", Username: "alice"}
	snapshot, err := store.NewSnapshot("https://jira.example.test/jira", principal, []Field{
		{ID: "customfield_2", Name: "Two", Alias: "two", Custom: true, Type: "number"},
		{ID: "customfield_1", Name: "One", Alias: "one", Custom: true, Type: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Write(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "\n  \"schemaVersion\": 2,") {
		t.Fatalf("snapshot is not pretty JSON: %s", contents)
	}
	got, gotPath, err := store.Read(snapshot.Instance, principal)
	if err != nil || gotPath != path {
		t.Fatalf("Read = %#v, %q, %v", got, gotPath, err)
	}
	if got.SchemaVersion != schemaVersion || got.Fields[0].ID != "customfield_1" || got.Fields[1].ID != "customfield_2" {
		t.Fatalf("round trip = %#v", got)
	}
	if !store.Fresh(got) || store.Stale(got) {
		t.Fatal("fresh snapshot reported stale")
	}
	store.Now = func() time.Time { return now.Add(cacheTTL) }
	if store.Fresh(got) || !store.Stale(got) {
		t.Fatal("snapshot exactly at expiry must be stale")
	}
}

func TestReadMissingInvalidAndIdentityMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := New(t.TempDir(), func() time.Time { return now })
	instance := "https://jira.example.test"
	principal := Principal{AccountID: "1", Username: "alice"}
	_, path, err := store.Read(instance, principal)
	if !errors.Is(err, ErrNotFound) || path == "" {
		t.Fatalf("missing Read path=%q err=%v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Read(instance, principal)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid Read error = %v", err)
	}
	snapshot, err := store.NewSnapshot(instance, Principal{AccountID: "2", Username: "alice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Put a valid-but-wrong identity record at principal 1's path to prove Read
	// validates content rather than trusting the filename.
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Read(instance, principal)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestReadRejectsLegacyCustomOnlySnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := New(t.TempDir(), func() time.Time { return now })
	instance := "https://jira.example.test"
	principal := Principal{AccountID: "1", Username: "alice"}
	path, err := store.Path(instance, principal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{
		"schemaVersion": 1,
		"instance": "https://jira.example.test",
		"principal": {"accountId": "1", "username": "alice"},
		"fetchedAt": "2026-07-30T12:00:00Z",
		"expiresAt": "2026-07-31T12:00:00Z",
		"fields": [{"id": "customfield_10001", "name": "Sprint", "alias": "sprint"}]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read(instance, principal); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy snapshot error = %v", err)
	}
}

func TestPermissionsAndAtomicReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not portable on Windows")
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := New(t.TempDir(), func() time.Time { return now })
	principal := Principal{Username: "alice"}
	first, err := store.NewSnapshot("https://jira.example.test", principal, []Field{{ID: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Write(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{store.Root, filepath.Join(store.Root, "fields"), filepath.Join(store.Root, "fields", "hosts"), path} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o700)
		if name == path {
			want = 0o600
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("permissions %q = %#o, want %#o", name, got, want)
		}
	}
	second, err := store.NewSnapshot(first.Instance, principal, []Field{{ID: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(second); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Read(first.Instance, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fields) != 1 || got.Fields[0].ID != "two" {
		t.Fatalf("replacement result = %#v", got.Fields)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".fieldcache-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}
}
