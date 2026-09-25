package cmd

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This repository becomes public, so no file in it may name internal
// infrastructure or carry a credential. The fixture guard in fixture_test.go
// checks the captured response bodies; this one checks every file, because the
// first real leak was an inline test body rather than a fixture and so went
// unseen.
//
// Patterns are deliberately narrow. A guard that cries wolf gets deleted.
func TestRepositoryNamesNothingInternal(t *testing.T) {
	banned := []struct {
		what string
		re   *regexp.Regexp
	}{
		{"a real storage bucket, use example-bucket", regexp.MustCompile(`(?i)\b(s3|gs)://intuizi`)},
		{"a staging hostname", regexp.MustCompile(`(?i)` + `staging` + `-env-console`)},
		{"an internal address, use user@example.com", regexp.MustCompile(`(?i)[a-z0-9._%+-]+@intuizi\.com`)},
		{"a private key", regexp.MustCompile(`BEGIN [A-Z ]*PRIVATE KEY`)},
		{"an npm token", regexp.MustCompile(`npm` + `_[A-Za-z0-9]{30,}`)},
		{"a GitHub token", regexp.MustCompile(`gh[pousr]` + `_[A-Za-z0-9]{30,}`)},
		{"an AWS key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	}

	// Two addresses are published on purpose: the release bot's commit identity
	// in .goreleaser.yaml, and the security contact. Everything else at the
	// company domain is a person, and people do not belong in a public repo.
	allowed := regexp.MustCompile(`(?i)(noreply|security)@intuizi\.com`)

	skipDir := map[string]bool{".git": true, "bin": true, "dist": true, "node_modules": true, "live-results": true}

	root := ".." // tests run in the package directory
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		// This file names the patterns it looks for.
		if d.Name() == "repo_scrub_test.go" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Binary files hold no reviewable text; a NUL byte is the giveaway.
		if bytes.IndexByte(body, 0) >= 0 {
			return nil
		}

		rel := strings.TrimPrefix(filepath.ToSlash(path), "../")
		for _, b := range banned {
			for _, m := range b.re.FindAllIndex(body, -1) {
				hit := string(body[m[0]:m[1]])
				if allowed.MatchString(hit) {
					continue
				}
				line := 1 + bytes.Count(body[:m[0]], []byte("\n"))
				t.Errorf("%s:%d carries %s: %q", rel, line, b.what, hit)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}
