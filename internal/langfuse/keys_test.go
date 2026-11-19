package langfuse

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOf(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// repoWithDir returns a git repository root and a directory two levels
// inside it.
func repoWithDir(t *testing.T) (root, dir string) {
	t.Helper()
	outside := t.TempDir()
	root = filepath.Join(outside, "repo")
	dir = filepath.Join(root, "apps", "agent")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

const dotenvKeys = "LANGFUSE_PUBLIC_KEY=pk-file\nLANGFUSE_SECRET_KEY=sk-file\n"

func TestFindKeysPrefersTheShellEnvironment(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(root, ".env"), dotenvKeys)

	keys, err := FindKeys(FindOptions{
		LookupEnv: envOf(map[string]string{PublicKeyVar: "pk-env", SecretKeyVar: "sk-env", HostVar: "https://us.cloud.langfuse.com/"}),
		Dir:       dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys.PublicKey.Reveal() != "pk-env" || keys.SecretKey.Reveal() != "sk-env" || keys.Source.Kind != SourceEnvironment {
		t.Fatalf("keys from %v", keys.Source)
	}
	if keys.BaseURL != "https://us.cloud.langfuse.com" {
		t.Fatalf("base URL = %q", keys.BaseURL)
	}
}

func TestFindKeysSkipsAnEnvironmentWithOnlyOneKey(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(root, ".env"), dotenvKeys)

	keys, err := FindKeys(FindOptions{LookupEnv: envOf(map[string]string{PublicKeyVar: "pk-env"}), Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if keys.Source.Path != filepath.Join(root, ".env") || keys.PublicKey.Reveal() != "pk-file" {
		t.Fatalf("keys from %v", keys.Source)
	}
}

func TestFindKeysUsesTheNearestDotenvThatHasThem(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(root, ".env"), "LANGFUSE_PUBLIC_KEY=pk-root\nLANGFUSE_SECRET_KEY=sk-root\n")
	writeFile(t, filepath.Join(root, "apps", ".env"), "LANGFUSE_PUBLIC_KEY=pk-apps\nLANGFUSE_SECRET_KEY=sk-apps\n")
	// The nearest .env lacks the keys, so the search goes on up.
	writeFile(t, filepath.Join(dir, ".env"), "OTHER=1\n")

	keys, err := FindKeys(FindOptions{LookupEnv: envOf(nil), Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if keys.Source.Path != filepath.Join(root, "apps", ".env") || keys.PublicKey.Reveal() != "pk-apps" {
		t.Fatalf("keys from %v", keys.Source)
	}
	if keys.BaseURL != DefaultBaseURL {
		t.Fatalf("base URL = %q", keys.BaseURL)
	}
}

func TestFindKeysStopsAtTheRepositoryRoot(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(filepath.Dir(root), ".env"), dotenvKeys)

	_, err := FindKeys(FindOptions{LookupEnv: envOf(nil), Dir: dir})
	if !errors.Is(err, ErrNoKeys) {
		t.Fatalf("err = %v, want ErrNoKeys", err)
	}
	if strings.Contains(err.Error(), filepath.Join(filepath.Dir(root), ".env")) {
		t.Fatalf("looked outside the repository: %v", err)
	}
}

func TestFindKeysOutsideARepositoryLooksOnlyInTheDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "project")
	writeFile(t, filepath.Join(parent, ".env"), dotenvKeys)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := FindKeys(FindOptions{LookupEnv: envOf(nil), Dir: dir}); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("err = %v, want ErrNoKeys", err)
	}
}

func TestFindKeysWithAFileReadsOnlyThatFile(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(root, ".env"), dotenvKeys)
	custom := filepath.Join(dir, "langfuse.env")
	writeFile(t, custom, "LANGFUSE_PUBLIC_KEY=\"pk-custom\"\nLANGFUSE_SECRET_KEY='sk-custom'\nLANGFUSE_BASE_URL=\"https://jp.cloud.langfuse.com\"\n")

	keys, err := FindKeys(FindOptions{
		LookupEnv: envOf(map[string]string{PublicKeyVar: "pk-env", SecretKeyVar: "sk-env"}),
		Dir:       dir,
		File:      custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if keys.PublicKey.Reveal() != "pk-custom" || keys.SecretKey.Reveal() != "sk-custom" || keys.BaseURL != "https://jp.cloud.langfuse.com" {
		t.Fatalf("keys from %v", keys.Source)
	}
	if keys.Source != (Source{Kind: SourceFile, Path: custom}) {
		t.Fatalf("source = %+v", keys.Source)
	}

	missing := filepath.Join(dir, "missing.env")
	if _, err := FindKeys(FindOptions{File: missing}); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v", err)
	}
	empty := filepath.Join(dir, "empty.env")
	writeFile(t, empty, "LANGFUSE_PUBLIC_KEY=pk\n")
	if _, err := FindKeys(FindOptions{File: empty}); err == nil || !strings.Contains(err.Error(), "does not set both") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindKeysRefusesABadBaseURL(t *testing.T) {
	for _, base := range []string{"cloud.langfuse.com", "ftp://cloud.langfuse.com", "https://user:hunter2@cloud.langfuse.com", "ftp://user:hunter2@cloud.langfuse.com"} {
		_, err := FindKeys(FindOptions{LookupEnv: envOf(map[string]string{PublicKeyVar: "pk", SecretKeyVar: "sk", BaseURLVar: base})})
		if err == nil {
			t.Errorf("%s: no error", base)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error repeats the URL's password: %v", base, err)
		}
	}
}

func TestParseDotenv(t *testing.T) {
	values := parseDotenv(strings.Join([]string{
		"# comment",
		"export A=plain value # comment",
		`B="double \"quoted\" # not a comment" # comment`,
		`C='single #literal \n'`,
		"D = spaced ",
		`E="line\nbreak"`,
		"F=\r",
		"not a line",
		"A=later wins",
	}, "\n"))
	want := map[string]string{
		"A": "later wins",
		"B": `double "quoted" # not a comment`,
		"C": `single #literal \n`,
		"D": "spaced",
		"E": "line\nbreak",
		"F": "",
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s = %q, want %q", key, values[key], value)
		}
	}
	if len(values) != len(want) {
		t.Errorf("values = %v", values)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	keys := Keys{PublicKey: NewSecret("pk-lf-visible"), SecretKey: NewSecret("sk-lf-visible"), BaseURL: DefaultBaseURL}
	raw, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		fmt.Sprint(keys), fmt.Sprintf("%v %+v %#v %s", keys, keys, keys, keys.SecretKey), string(raw),
	} {
		if strings.Contains(text, "visible") {
			t.Fatalf("key printed: %s", text)
		}
	}
}

func TestAFileWithoutAHostFallsBackToTheShellHost(t *testing.T) {
	root, dir := repoWithDir(t)
	writeFile(t, filepath.Join(root, ".env"), dotenvKeys)

	for name, tc := range map[string]struct {
		env      map[string]string
		wantBase string
		wantFrom Source
	}{
		"base URL": {map[string]string{BaseURLVar: "https://us.cloud.langfuse.com", HostVar: "https://jp.cloud.langfuse.com"}, "https://us.cloud.langfuse.com", Source{Kind: SourceEnvironment}},
		"host":     {map[string]string{HostVar: "https://jp.cloud.langfuse.com"}, "https://jp.cloud.langfuse.com", Source{Kind: SourceEnvironment}},
		"default":  {nil, DefaultBaseURL, Source{Kind: SourceDefault}},
	} {
		t.Run(name, func(t *testing.T) {
			keys, err := FindKeys(FindOptions{LookupEnv: envOf(tc.env), Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if keys.Source.Path != filepath.Join(root, ".env") || keys.BaseURL != tc.wantBase || keys.HostSource != tc.wantFrom {
				t.Fatalf("keys from %v, host %s from %v", keys.Source, keys.BaseURL, keys.HostSource)
			}
		})
	}
}

func TestAHostInTheFileWinsOverTheShell(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "langfuse.env")
	writeFile(t, file, dotenvKeys+"LANGFUSE_HOST=https://us.cloud.langfuse.com\n")

	keys, err := FindKeys(FindOptions{LookupEnv: envOf(map[string]string{BaseURLVar: "https://jp.cloud.langfuse.com"}), File: file})
	if err != nil {
		t.Fatal(err)
	}
	if keys.BaseURL != "https://us.cloud.langfuse.com" || keys.HostSource != keys.Source {
		t.Fatalf("host %s from %v", keys.BaseURL, keys.HostSource)
	}
}

func TestShellHostOnly(t *testing.T) {
	host, from, err := shellHost(envOf(map[string]string{HostVar: "https://us.cloud.langfuse.com/"}))
	if err != nil || host != "https://us.cloud.langfuse.com" || from.Kind != SourceEnvironment {
		t.Fatalf("host %q from %v, %v", host, from, err)
	}
	host, from, err = shellHost(envOf(nil))
	if err != nil || host != DefaultBaseURL || from.Kind != SourceDefault {
		t.Fatalf("host %q from %v, %v", host, from, err)
	}
}

func TestCheckRegionRefusesHIPAA(t *testing.T) {
	for _, base := range []string{"https://hipaa.cloud.langfuse.com", "https://HIPAA.cloud.langfuse.com:443"} {
		if err := CheckRegion(base); err == nil || !strings.Contains(err.Error(), "HIPAA region") {
			t.Errorf("%s: err = %v", base, err)
		}
	}
	for _, base := range []string{DefaultBaseURL, "https://us.cloud.langfuse.com", "https://langfuse.example.com"} {
		if err := CheckRegion(base); err != nil {
			t.Errorf("%s: err = %v", base, err)
		}
	}
}
