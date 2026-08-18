// Package sentineltest provides a stand-in for the Sentinel CLI. The real
// binary is not redistributable, so tests that need to exercise a Sentinel run
// drive this fake instead.
//
// It deliberately does not import the sentinel package, so that the in-package
// tests of that package can use it without an import cycle.
package sentineltest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ConfigFileName mirrors sentinel.ConfigFileName. It is duplicated rather than
// imported to keep this package free of a dependency on the package under test.
const ConfigFileName = "sentinel.hcl"

// The reports below mirror documents captured from a real `sentinel test -json`
// run (Sentinel v0.41.0), trimmed of the per-case "trace" object which the
// provider does not read. Keeping the exact key names and null-versus-empty
// choices matters: a failing case reports null "errors" and explains itself
// through "rule_detail", and a policy that failed to parse reports null "cases"
// while a policy with no tests reports an empty array.

// PassingReport is a `sentinel test -json` document where everything passes.
const PassingReport = `{
  "policies": {
    "restrict-instance-type.sentinel": {
      "path": "restrict-instance-type.sentinel",
      "status": "PASS",
      "errors": null,
      "duration": 20,
      "cases": {
        "test/restrict-instance-type/pass.hcl": {
          "path": "test/restrict-instance-type/pass.hcl",
          "status": "PASS",
          "errors": null,
          "rule_detail": {},
          "config_warnings": null,
          "config_legacy": false
        },
        "test/restrict-instance-type/fail.hcl": {
          "path": "test/restrict-instance-type/fail.hcl",
          "status": "PASS",
          "errors": null,
          "rule_detail": {},
          "config_warnings": null,
          "config_legacy": false
        }
      }
    }
  },
  "duration": 42
}`

// FailingReport is a `sentinel test -json` document with one failing case. Note
// that "errors" is null: an assertion failure is described by "rule_detail",
// which maps the rule that failed to the reason it failed.
const FailingReport = `{
  "policies": {
    "require-tags.sentinel": {
      "path": "require-tags.sentinel",
      "status": "FAIL",
      "errors": null,
      "duration": 30,
      "cases": {
        "test/require-tags/pass.hcl": {
          "path": "test/require-tags/pass.hcl",
          "status": "FAIL",
          "errors": null,
          "rule_detail": {
            "main": "expected \"main\" to be true, got: false"
          },
          "config_warnings": null,
          "config_legacy": false
        },
        "test/require-tags/fail.hcl": {
          "path": "test/require-tags/fail.hcl",
          "status": "PASS",
          "errors": null,
          "rule_detail": {},
          "config_warnings": null,
          "config_legacy": false
        }
      }
    }
  },
  "duration": 30
}`

// ErrorReport is a `sentinel test -json` document where a policy failed to parse,
// so it carries a policy level error and its "cases" is null rather than empty.
const ErrorReport = `{
  "policies": {
    "broken.sentinel": {
      "path": "broken.sentinel",
      "status": "ERROR",
      "errors": ["broken.sentinel:1:16: expected operand, found 'EOF'"],
      "duration": 1,
      "cases": null
    }
  },
  "duration": 2
}`

// SkippedReport is a `sentinel test -json` document for a policy with no test
// cases, which Sentinel reports as "?" and treats as passing. Its "cases" comes
// back as an empty array rather than an empty object.
const SkippedReport = `{
  "policies": {
    "untested.sentinel": {
      "path": "untested.sentinel",
      "status": "?",
      "errors": null,
      "duration": 0,
      "cases": []
    }
  },
  "duration": 1
}`

// CLI is a fake Sentinel CLI: it records how it was invoked, replays canned
// output and exits with a chosen status.
type CLI struct {
	// Binary is the path to pass to the runner, or to SENTINEL_BINARY.
	Binary string

	logs string
}

// NewCLI writes a fake CLI that prints stdout and stderr then exits with
// exitCode. The test is skipped on Windows, where the fake shell script cannot
// run.
func NewCLI(t *testing.T, stdout, stderr string, exitCode int) *CLI {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}

	dir := t.TempDir()
	cli := &CLI{
		Binary: filepath.Join(dir, "sentinel"),
		logs:   filepath.Join(dir, "invocation.log"),
	}

	stdoutFile := filepath.Join(dir, "stdout")
	stderrFile := filepath.Join(dir, "stderr")
	WriteFile(t, stdoutFile, stdout)
	WriteFile(t, stderrFile, stderr)

	script := fmt.Sprintf(`#!/bin/sh
{
  echo "args: $*"
  echo "pwd: $(pwd -P)"
} > %[1]q
cat %[2]q
cat %[3]q >&2
exit %[4]d
`, cli.logs, stdoutFile, stderrFile, exitCode)

	WriteFile(t, cli.Binary, script)
	if err := os.Chmod(cli.Binary, 0o755); err != nil {
		t.Fatalf("chmod: %s", err)
	}

	return cli
}

// Invocation returns the arguments and working directory the fake CLI saw. It
// fails the test when the CLI was never invoked.
func (c *CLI) Invocation(t *testing.T) (args, dir string) {
	t.Helper()

	raw, err := os.ReadFile(c.logs)
	if err != nil {
		t.Fatalf("the fake CLI was never invoked: %s", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "args: "):
			args = strings.TrimPrefix(line, "args: ")
		case strings.HasPrefix(line, "pwd: "):
			dir = strings.TrimPrefix(line, "pwd: ")
		}
	}
	return args, dir
}

// PolicyDir creates a directory holding a minimal sentinel.hcl, which is what
// the runner requires before it will invoke the CLI.
func PolicyDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	WriteFile(t, filepath.Join(dir, ConfigFileName), `policy "a" {
  source = "./a.sentinel"
}
`)
	return dir
}

// WriteFile writes content to path, failing the test on error.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %s", path, err)
	}
}

// ResolveDir resolves symlinks in dir, so that a path can be compared with the
// working directory a subprocess reported. On macOS the temporary directory is
// reached through a symlink.
func ResolveDir(t *testing.T, dir string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}
