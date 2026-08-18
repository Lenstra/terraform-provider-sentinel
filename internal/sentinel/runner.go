package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BinaryEnvVar names the environment variable used to point at the Sentinel
// CLI. Provider defined functions have no access to the provider configuration,
// so an environment variable is the only way to override the binary.
const BinaryEnvVar = "SENTINEL_BINARY"

// DefaultBinary is the binary looked up in PATH when BinaryEnvVar is unset.
const DefaultBinary = "sentinel"

// Statuses reported by `sentinel test`. A policy may also report StatusSkip,
// which marks a policy with no test cases and which Sentinel treats as passing.
const (
	StatusPass  = "PASS"
	StatusFail  = "FAIL"
	StatusError = "ERROR"
	StatusSkip  = "?"
)

// Runner executes the Sentinel CLI.
type Runner struct {
	// Binary is the Sentinel CLI to run. When empty, ResolveBinary is used.
	Binary string
}

// Report is the outcome of a `sentinel test` run.
type Report struct {
	// Passed reports whether every test passed. It is true when the CLI exited
	// zero and the JSON report, when it could be decoded, listed no failure.
	// Requiring both fails closed: this drives a Terraform precondition, so a
	// disagreement between the two must never let a failing policy through.
	Passed bool
	// ExitCode is the CLI exit code.
	ExitCode int
	// Decoded reports whether the JSON report could be read. When false, only
	// Passed, ExitCode and Output carry information.
	Decoded bool
	// DurationMs is the total run time the CLI reported, when it reported one.
	// It is deliberately not surfaced to Terraform: a provider defined function
	// has to return the same result at plan and at apply time.
	DurationMs int64
	// Policies are the per-policy results, sorted by path. Empty when the JSON
	// report could not be decoded.
	Policies []PolicyResult
	// Failures are one-line descriptions of every failing test case, sorted, for
	// surfacing in a Terraform error message.
	Failures []string
	// Output is the raw CLI output, used as a fallback when the JSON report is
	// unusable.
	Output string
}

// PolicyResult is the result of testing a single policy.
type PolicyResult struct {
	// Path is the path of the policy file.
	Path string
	// Status is PASS, FAIL, ERROR, or "?" when the policy has no test cases.
	Status string
	// Errors are messages from processing the policy itself, typically parser
	// errors, as opposed to a case failing.
	Errors []string
	// Cases are the policy's test cases, sorted by path.
	Cases []CaseResult
}

// CaseResult is the result of a single test case of a policy.
type CaseResult struct {
	// Path is the path of the test case file.
	Path string
	// Status is PASS, FAIL or ERROR.
	Status string
	// Errors are messages from running this case.
	Errors []string
	// RuleFailures maps the name of a rule whose assertion failed to why it
	// failed, for instance "main" to `expected "main" to be true, got: false`.
	// Sentinel only reports failures here, so a rule absent from the map either
	// passed or was not asserted. This is where a failing case explains itself:
	// Errors stays empty for an assertion failure.
	RuleFailures map[string]string
}

// passed reports whether a status counts as a pass. Sentinel treats "?", a
// policy with no cases, as passing.
func passed(status string) bool {
	return status == StatusPass || status == StatusSkip
}

// Counts summarises a report in test cases. A policy with no cases counts as one
// entry, so that a policy which failed to parse is still visible in the totals.
func (r *Report) Counts() (total, passedCount, failed int) {
	for _, policy := range r.Policies {
		if len(policy.Cases) == 0 {
			total++
			if passed(policy.Status) {
				passedCount++
			} else {
				failed++
			}
			continue
		}
		for _, testCase := range policy.Cases {
			total++
			if passed(testCase.Status) {
				passedCount++
			} else {
				failed++
			}
		}
	}
	return total, passedCount, failed
}

// ResolveBinary returns the Sentinel CLI to run, honouring BinaryEnvVar.
func ResolveBinary() (string, error) {
	if binary := os.Getenv(BinaryEnvVar); binary != "" {
		// An explicit override may be a bare command name or a path; only look
		// it up in PATH when it has no separator.
		if !strings.ContainsRune(binary, filepath.Separator) {
			return exec.LookPath(binary)
		}
		if _, err := os.Stat(binary); err != nil {
			return "", fmt.Errorf("%s points at %q which cannot be used: %w", BinaryEnvVar, binary, err)
		}
		return binary, nil
	}

	path, err := exec.LookPath(DefaultBinary)
	if err != nil {
		return "", fmt.Errorf(
			"could not find the %q binary in PATH, set %s to its location: %w",
			DefaultBinary, BinaryEnvVar, err,
		)
	}
	return path, nil
}

// Test runs `sentinel test -json` in dir, which must be a directory holding a
// sentinel.hcl file. The returned error covers only failures to run the CLI at
// all; a run that completes with failing tests returns a report with Passed
// set to false and a nil error.
func (r *Runner) Test(ctx context.Context, dir string) (*Report, error) {
	if err := checkConfigDir(dir); err != nil {
		return nil, err
	}

	binary := r.Binary
	if binary == "" {
		var err error
		if binary, err = ResolveBinary(); err != nil {
			return nil, err
		}
	}

	var stdout, stderr bytes.Buffer
	// Sentinel resolves policy sources, modules and test cases relative to the
	// configuration file, so the CLI has to run in the policy directory.
	cmd := exec.CommandContext(ctx, binary, "test", "-json")
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	report := &Report{
		Output: combineOutput(stdout.String(), stderr.String()),
	}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		report.Passed = true
	case errors.As(runErr, &exitErr):
		report.ExitCode = exitErr.ExitCode()
	default:
		return nil, fmt.Errorf("failed to run %q test in %q: %w", binary, dir, runErr)
	}

	// The JSON report only adds detail. Sentinel documents its shape as subject
	// to change, so a decode failure must not turn a usable exit code into an
	// error.
	if err := report.decode(stdout.Bytes()); err == nil {
		report.Decoded = true
		report.collectFailures()
		if len(report.Failures) > 0 {
			report.Passed = false
		}
	}

	return report, nil
}

func checkConfigDir(dir string) error {
	if dir == "" {
		return errors.New("the policy directory must not be empty")
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot read the policy directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory, it must be the directory holding %s", dir, ConfigFileName)
	}

	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); err != nil {
		return fmt.Errorf("%q does not contain a %s file: %w", dir, ConfigFileName, err)
	}

	return nil
}

func combineOutput(stdout, stderr string) string {
	// Both streams are kept: stdout carries the JSON report and stderr the
	// messages Sentinel writes outside it, and when the report cannot be decoded
	// this is the only description of the run left to show.
	parts := make([]string, 0, 2)
	for _, part := range []string{strings.TrimSpace(stdout), strings.TrimSpace(stderr)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n")
}

// jsonReport mirrors the document written by `sentinel test -json`: an object
// whose top-level keys are "policies", keyed by policy path, and "duration" in
// milliseconds.
//
// Sentinel documents the format as subject to change, so the collections are
// decoded permissively: each entry carries its own path, which lets an array
// encoding be read as well as the documented object encoding.
type jsonReport struct {
	Duration int64           `json:"duration"`
	Policies json.RawMessage `json:"policies"`
}

type jsonPolicy struct {
	Path     string          `json:"path"`
	Status   string          `json:"status"`
	Errors   []string        `json:"errors"`
	Duration int64           `json:"duration"`
	Cases    json.RawMessage `json:"cases"`
}

type jsonCase struct {
	Path   string   `json:"path"`
	Status string   `json:"status"`
	Errors []string `json:"errors"`
	// RuleDetail holds assertion failure detail indexed by rule name. Sentinel
	// writes the message as a plain string, but documents the format as subject
	// to change, so it is decoded loosely.
	RuleDetail map[string]json.RawMessage `json:"rule_detail"`
}

// ruleFailures turns the reported rule detail into a rule to message map.
func ruleFailures(detail map[string]json.RawMessage) map[string]string {
	if len(detail) == 0 {
		return nil
	}

	failures := make(map[string]string, len(detail))
	for rule, raw := range detail {
		var message string
		if err := json.Unmarshal(raw, &message); err != nil {
			// Not a plain string. Keep the raw JSON rather than dropping the
			// only explanation of the failure.
			message = string(raw)
		}
		failures[rule] = singleLine(message)
	}
	return failures
}

func (r *Report) decode(stdout []byte) error {
	stdout = bytes.TrimSpace(stdout)
	if len(stdout) == 0 {
		return errors.New("the CLI produced no JSON report")
	}

	var raw jsonReport
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return fmt.Errorf("failed to decode the JSON report: %w", err)
	}

	policies, err := decodeNamed[jsonPolicy](raw.Policies)
	if err != nil {
		return fmt.Errorf("failed to decode the reported policies: %w", err)
	}

	// Built up separately so that a failure part way through leaves the report
	// untouched, rather than half filled in.
	results := make([]PolicyResult, 0, len(policies))

	for _, entry := range policies {
		policy := PolicyResult{
			Path:   firstNonEmpty(entry.value.Path, entry.name),
			Status: entry.value.Status,
			Errors: entry.value.Errors,
		}

		cases, err := decodeNamed[jsonCase](entry.value.Cases)
		if err != nil {
			return fmt.Errorf("failed to decode the cases of policy %q: %w", policy.Path, err)
		}
		for _, entry := range cases {
			result := CaseResult{
				Path:         firstNonEmpty(entry.value.Path, entry.name),
				Status:       entry.value.Status,
				Errors:       entry.value.Errors,
				RuleFailures: ruleFailures(entry.value.RuleDetail),
			}
			policy.Cases = append(policy.Cases, result)
		}
		sort.Slice(policy.Cases, func(i, j int) bool { return policy.Cases[i].Path < policy.Cases[j].Path })

		results = append(results, policy)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })

	r.DurationMs = raw.Duration
	r.Policies = results

	return nil
}

// namedEntry pairs a decoded value with the key it was stored under, so that
// object and array encodings of the same collection can be handled alike.
type namedEntry[T any] struct {
	name  string
	value T
}

// decodeNamed decodes a collection Sentinel documents as an object keyed by
// path, tolerating an array of objects that carry their own path.
func decodeNamed[T any](raw json.RawMessage) ([]namedEntry[T], error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	switch raw[0] {
	case '{':
		var byName map[string]T
		if err := json.Unmarshal(raw, &byName); err != nil {
			return nil, err
		}
		entries := make([]namedEntry[T], 0, len(byName))
		for name, value := range byName {
			entries = append(entries, namedEntry[T]{name: name, value: value})
		}
		return entries, nil
	case '[':
		var list []T
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		entries := make([]namedEntry[T], 0, len(list))
		for _, value := range list {
			entries = append(entries, namedEntry[T]{value: value})
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("expected an object or an array, got %s", string(raw))
	}
}

// collectFailures turns the report into one line per failure, which is what a
// precondition error message shows.
func (r *Report) collectFailures() {
	for _, policy := range r.Policies {
		// A policy level error, a parse failure for instance, is reported
		// against the policy rather than any single case.
		if len(policy.Cases) == 0 && !passed(policy.Status) {
			r.Failures = append(r.Failures, describeFailure(policy.Path, policy.Status, policy.Errors, nil))
			continue
		}
		for _, testCase := range policy.Cases {
			if passed(testCase.Status) {
				continue
			}
			where := policy.Path + ": " + testCase.Path
			r.Failures = append(r.Failures, describeFailure(
				where, testCase.Status, testCase.Errors, testCase.RuleFailures,
			))
		}
	}
	sort.Strings(r.Failures)
}

// describeFailure builds a single readable line for one failure.
func describeFailure(where, status string, errs []string, rules map[string]string) string {
	failure := where + ": " + status

	var detail []string
	for _, err := range errs {
		if trimmed := singleLine(err); trimmed != "" {
			detail = append(detail, trimmed)
		}
	}
	// An assertion failure explains itself through the rule detail rather than
	// through errs, so this is usually the only description of what went wrong.
	for _, rule := range sortedKeys(rules) {
		detail = append(detail, rule+": "+rules[rule])
	}

	if len(detail) > 0 {
		failure += ": " + strings.Join(detail, "; ")
	}
	return failure
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}

	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// singleLine collapses a multi line CLI message so it stays readable inside a
// Terraform diagnostic.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
