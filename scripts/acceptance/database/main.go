// database is an opt-in acceptance driver, not a product command. It links the
// frozen candidate's database/business packages in a separate build directory.
// Credentials travel only through inherited anonymous stdin pipes. Raw worker
// output, database errors and profile values are never persisted or forwarded.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/dbprofile"
	charmLog "github.com/charmbracelet/log"
)

type options struct {
	profile, alias, output, stage, work             string
	executable, executableHash, stopScript, version string
	connect, selfTest, worker                       bool
}

type check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type result struct {
	Status           string  `json:"status"`
	Alias            string  `json:"alias"`
	Type             string  `json:"type,omitempty"`
	Stage            string  `json:"stage,omitempty"`
	Error            string  `json:"error,omitempty"`
	EngineVersion    string  `json:"engine_version,omitempty"`
	RestoreAvailable bool    `json:"restore_available"`
	Checks           []check `json:"checks,omitempty"`
	DurationMS       int64   `json:"duration_ms"`
}

type workerInput struct {
	Profile dbprofile.Profile
}

var safeName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,80}$`)
var numericVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)

func main() {
	// Save the result pipe, then silence every ordinary logging path before
	// reading any profile. A panic/fatal worker's OS stderr is also discarded
	// by its parent. Only typed, validated JSON crosses the output boundary.
	output := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		os.Exit(1)
	}
	os.Stdout, os.Stderr = devnull, devnull
	log.SetOutput(io.Discard)
	charmLog.SetOutput(io.Discard)
	r, code := execute()
	_ = json.NewEncoder(output).Encode(r)
	os.Exit(code)
}

func execute() (r result, code int) {
	r.Status, r.Error, code = "failed", "test_failed", 1
	started := time.Now()
	defer func() {
		if recover() != nil {
			r.Status, r.Error, code = "failed", "worker_exception", 1
		}
		r.DurationMS = time.Since(started).Milliseconds()
	}()
	var o options
	f := flag.NewFlagSet("database-acceptance", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.profile, "profile", "", "external profile path")
	f.StringVar(&o.alias, "alias", "", "roam-mysql-test or roam-postgres-test")
	f.StringVar(&o.output, "output", "", "new evidence directory")
	f.StringVar(&o.stage, "stage", "", "internal worker stage")
	f.StringVar(&o.work, "work", "", "internal work directory")
	f.StringVar(&o.executable, "executable", "", "frozen Windows executable")
	f.StringVar(&o.executableHash, "executable-sha256", "", "expected executable digest")
	f.StringVar(&o.stopScript, "stop-script", "", "reviewed normal-stop helper")
	f.StringVar(&o.version, "expected-version", "", "expected candidate version")
	f.BoolVar(&o.connect, "connect", false, "explicitly enable authorized database access")
	f.BoolVar(&o.selfTest, "self-test", false, "isolated SQLite rehearsal; no real profile")
	f.BoolVar(&o.worker, "worker", false, "internal child process")
	if f.Parse(os.Args[1:]) != nil || f.NArg() != 0 || (o.selfTest && o.connect) {
		r.Error = "invalid_arguments"
		return
	}
	if !o.selfTest && !o.connect {
		r.Status, r.Error = "not_run", "connect_flag_required"
		return
	}
	if o.selfTest {
		o.alias = "synthetic-sqlite"
	} else if o.alias != "roam-mysql-test" && o.alias != "roam-postgres-test" {
		r.Error = "alias_invalid"
		return
	}
	r.Alias = o.alias
	if o.worker {
		var input workerInput
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			r.Error = "worker_input_invalid"
			return
		}
		r = runStage(o, input.Profile)
		if r.Status == "passed" {
			code = 0
		}
		return
	}
	if o.output == "" {
		r.Error = "output_required"
		return
	}
	work, err := filepath.Abs(o.output)
	if err != nil || os.Mkdir(work, 0o700) != nil {
		r.Error = "output_must_be_new"
		return
	}
	o.work = work
	p := dbprofile.Profile{}
	if !o.selfTest {
		if o.profile == "" {
			r.Error = "profile_path_required"
			return
		}
		// This is the only real profile read. Its selected target is held in
		// memory for the entire run, preventing target changes between stages.
		p, err = dbprofile.LoadFile(o.profile, o.alias)
		if err != nil {
			r.Error = classify(err)
			return
		}
		if (o.alias == "roam-mysql-test" && p.Type != "mysql") ||
			(o.alias == "roam-postgres-test" && p.Type != "postgres") {
			r.Error = "alias_type_mismatch"
			return
		}
	}
	r.Type = p.Type
	if o.selfTest {
		r.Type = "sqlite"
	}
	r.RestoreAvailable = o.selfTest || p.RestoreDatabase != ""
	stages := []string{"preflight", "seed", "restart_primary", "product_restart_primary"}
	if r.RestoreAvailable {
		stages = append(stages, "restore", "restart_restore")
	}
	for _, stage := range stages {
		child := invokeWorker(o, p, stage)
		if err := writeNewJSON(filepath.Join(work, stage+".json"), child); err != nil {
			r.Error = "evidence_write_failed"
			return
		}
		r.Checks = append(r.Checks, child.Checks...)
		if child.EngineVersion != "" {
			r.EngineVersion = child.EngineVersion
		}
		if child.Status != "passed" {
			r.Status, r.Error, r.Stage = "failed", child.Error, stage
			_ = writeNewJSON(filepath.Join(work, "result.json"), r)
			return
		}
	}
	r.Status, r.Error, code = "passed", "", 0
	if !r.RestoreAvailable {
		r.Status = "passed_with_pending_restore"
		r.Checks = append(r.Checks, check{Name: "empty_database_restore", Status: "pending", Error: "restore_database_missing"})
	}
	r.DurationMS = time.Since(started).Milliseconds()
	if writeNewJSON(filepath.Join(work, "result.json"), r) != nil {
		r.Status, r.Error, code = "failed", "evidence_write_failed", 1
	}
	return
}

func invokeWorker(o options, p dbprofile.Profile, stage string) result {
	r := result{Alias: o.alias, Stage: stage, Status: "failed", Error: "worker_failed"}
	executable, err := os.Executable()
	if err != nil {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	args := []string{"--worker", "--alias", o.alias, "--stage", stage, "--work", o.work}
	args = append(args, "--executable", o.executable, "--executable-sha256", o.executableHash, "--stop-script", o.stopScript, "--expected-version", o.version)
	if o.selfTest {
		args = append(args, "--self-test")
	} else {
		args = append(args, "--connect")
	}
	command := exec.CommandContext(ctx, executable, args...)
	input, err := json.Marshal(workerInput{Profile: p})
	if err != nil {
		return r
	}
	command.Stdin = bytes.NewReader(input)
	command.Stderr = io.Discard
	var buffer limitedBuffer
	command.Stdout = &buffer
	err = command.Run()
	if ctx.Err() != nil {
		r.Error = "worker_timeout"
		return r
	}
	var child result
	decoder := json.NewDecoder(bytes.NewReader(buffer.data))
	decoder.DisallowUnknownFields()
	if buffer.overflow || decoder.Decode(&child) != nil || !validResult(child, o.alias, stage) {
		r.Error = "worker_output_rejected"
		return r
	}
	if decoder.Decode(new(any)) != io.EOF || (err != nil && child.Status == "passed") {
		r.Error = "worker_output_rejected"
		return r
	}
	return child
}

type limitedBuffer struct {
	data     []byte
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const max = 1 << 20
	n := len(p)
	if len(b.data)+n > max {
		b.overflow = true
		return n, nil
	}
	b.data = append(b.data, p...)
	return n, nil
}

func validResult(r result, alias, stage string) bool {
	if r.Alias != alias || r.Stage != stage ||
		(r.Type != "sqlite" && r.Type != "mysql" && r.Type != "postgres" && r.Type != "") ||
		(r.Status != "passed" && r.Status != "failed") || !safeError(r.Error) ||
		(r.EngineVersion != "" && !numericVersion.MatchString(r.EngineVersion)) {
		return false
	}
	for _, c := range r.Checks {
		if !safeName.MatchString(c.Name) || !safeError(c.Error) ||
			(c.Status != "passed" && c.Status != "failed" && c.Status != "pending") {
			return false
		}
	}
	return true
}

func safeError(value string) bool {
	switch value {
	case "", "test_failed", "validation_failed", "profile_missing", "profile_invalid", "alias_missing",
		"authentication_failed", "tls_validation_failed", "connection_timeout", "connection_failed",
		"database_not_empty", "restore_database_missing", "worker_exception":
		return true
	case "database_syntax_error", "database_permission_denied", "database_constraint_failure":
		return true
	}
	return false
}

func classify(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, dbprofile.ErrProfileMissing):
		return "profile_missing"
	case errors.Is(err, dbprofile.ErrAliasMissing):
		return "alias_missing"
	case errors.Is(err, dbprofile.ErrInvalidProfile):
		return "profile_invalid"
	case errors.Is(err, errNotEmpty):
		return "database_not_empty"
	case errors.Is(err, errAssertion):
		return "validation_failed"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "x509"), strings.Contains(message, "tls"), strings.Contains(message, "ssl"), strings.Contains(message, "certificate"):
		return "tls_validation_failed"
	case strings.Contains(message, "password"), strings.Contains(message, "authentication"), strings.Contains(message, "access denied"), strings.Contains(message, "28p01"):
		return "authentication_failed"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "connection_timeout"
	case strings.Contains(message, "connection refused"), strings.Contains(message, "no such host"):
		return "connection_failed"
	case strings.Contains(message, "error 1064"), strings.Contains(message, "sqlstate 42601"):
		return "database_syntax_error"
	case strings.Contains(message, "permission denied"), strings.Contains(message, "command denied"):
		return "database_permission_denied"
	case strings.Contains(message, "foreign key constraint"), strings.Contains(message, "sqlstate 23503"):
		return "database_constraint_failure"
	default:
		return "test_failed"
	}
}

func writeNewJSON(filename string, value any) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}
