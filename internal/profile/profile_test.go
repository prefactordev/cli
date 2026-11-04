package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPathPrefersRepositoryRootFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "prefactor.json"), "{}")
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Locator{WorkingDir: nested, ExecutablePath: "/opt/prefactor/bin/prefactor"}.ConfigPath()
	if want := filepath.Join(repo, "prefactor.json"); got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}
}

func TestConfigPathFallsBackToInstallRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Locator{WorkingDir: repo, ExecutablePath: "/home/u/.prefactor/bin/prefactor"}.ConfigPath()
	if want := "/home/u/.prefactor/prefactor.json"; got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}

	got = Locator{WorkingDir: repo, ExecutablePath: "/tmp/build/prefactor"}.ConfigPath()
	if want := "/tmp/build/prefactor.json"; got != want {
		t.Fatalf("ConfigPath = %q, want %q", got, want)
	}
}

func TestLoadKeepsFileOrderAndSkipsInvalidEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefactor.json")
	writeFile(t, path, `{
  "zeta": {"api_key": "z", "base_url": "https://z.example"},
  "broken": {"api_key": 1, "base_url": "https://b.example"},
  "alpha": {"api_key": "a", "base_url": "https://a.example", "extra": true},
  "list": []
}`)

	entries := Load(path).Entries()
	if len(entries) != 2 || entries[0].Name != "zeta" || entries[1].Name != "alpha" {
		t.Fatalf("entries = %+v, want zeta then alpha", entries)
	}
}

func TestLoadTreatsMissingOrInvalidFileAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := Load(filepath.Join(dir, "missing.json")).Entries(); len(got) != 0 {
		t.Fatalf("missing file entries = %+v", got)
	}

	invalid := filepath.Join(dir, "prefactor.json")
	writeFile(t, invalid, "not json")
	if got := Load(invalid).Entries(); len(got) != 0 {
		t.Fatalf("invalid file entries = %+v", got)
	}
}

func TestSetWritesTheTypeScriptFormatWithPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "prefactor.json")
	store := Load(path)

	if err := store.Set("default", Profile{APIKey: "tok", BaseURL: "https://app.prefactorai.com"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("staging", Profile{APIKey: "s&t", BaseURL: "https://staging.example/?a=1&b=2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("default", Profile{APIKey: "tok2", BaseURL: "https://app.prefactorai.com"}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// JSON.stringify(profiles, null, 2) output from the TypeScript CLI.
	want := `{
  "default": {
    "api_key": "tok2",
    "base_url": "https://app.prefactorai.com"
  },
  "staging": {
    "api_key": "s&t",
    "base_url": "https://staging.example/?a=1&b=2"
  }
}`
	if string(raw) != want {
		t.Fatalf("file =\n%s\nwant\n%s", raw, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}

func TestRemoveReportsWhetherTheProfileExisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefactor.json")
	store := Load(path)
	if err := store.Set("a", Profile{APIKey: "k", BaseURL: "https://x.example"}); err != nil {
		t.Fatal(err)
	}

	removed, err := store.Remove("a")
	if err != nil || !removed {
		t.Fatalf("Remove(a) = %v, %v", removed, err)
	}
	removed, err = store.Remove("a")
	if err != nil || removed {
		t.Fatalf("second Remove(a) = %v, %v", removed, err)
	}

	raw, _ := os.ReadFile(path)
	if string(raw) != "{}" {
		t.Fatalf("file = %q, want {}", raw)
	}
}

func TestSaveAddsGitignoreEntryInRepositoryRoot(t *testing.T) {
	cases := map[string]struct {
		existing *string
		want     string
	}{
		"no gitignore":          {existing: nil, want: "prefactor.json\n"},
		"no trailing newline":   {existing: ptr("node_modules"), want: "node_modules\nprefactor.json\n"},
		"trailing newline":      {existing: ptr("node_modules\n"), want: "node_modules\nprefactor.json\n"},
		"already listed":        {existing: ptr("dist\nprefactor.json\n"), want: "dist\nprefactor.json\n"},
		"already listed rooted": {existing: ptr("/prefactor.json\n"), want: "/prefactor.json\n"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.existing != nil {
				writeFile(t, filepath.Join(repo, ".gitignore"), *tc.existing)
			}

			if err := Load(filepath.Join(repo, "prefactor.json")).Set("d", Profile{APIKey: "k", BaseURL: "https://x.example"}); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf(".gitignore = %q, want %q", raw, tc.want)
			}
		})
	}
}

func TestSaveOutsideRepositoryDoesNotWriteGitignore(t *testing.T) {
	dir := t.TempDir()
	if err := Load(filepath.Join(dir, "prefactor.json")).Set("d", Profile{APIKey: "k", BaseURL: "https://x.example"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf(".gitignore exists outside a repository: %v", err)
	}
}

func ptr(s string) *string { return &s }
