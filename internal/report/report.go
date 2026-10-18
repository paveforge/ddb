package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type Error struct {
	Code  int
	Phase string
	Err   error
}

func (e *Error) Error() string { return e.Phase + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
func Fail(code int, phase string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{code, phase, err}
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 3
}

type Summary struct {
	SchemaVersion    int     `json:"schema_version"`
	Command          string  `json:"command"`
	Table            string  `json:"table"`
	Region           string  `json:"region,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	Phase            string  `json:"phase"`
	Success          bool    `json:"success"`
	DryRun           bool    `json:"dry_run"`
	Validated        int64   `json:"validated"`
	Confirmed        int64   `json:"confirmed"`
	Unresolved       int64   `json:"unresolved"`
	NotAttempted     int64   `json:"not_attempted"`
	Deleted          int64   `json:"deleted"`
	Scanned          int64   `json:"scanned"`
	Bytes            int64   `json:"bytes"`
	Retries          int64   `json:"retries"`
	Capacity         float64 `json:"consumed_capacity"`
	Created          bool    `json:"table_created"`
	TruncateStarted  bool    `json:"truncate_started"`
	TruncateComplete bool    `json:"truncate_complete"`
	Recovery         string  `json:"recovery_file,omitempty"`
	RecoveryComplete bool    `json:"recovery_complete"`
	Partial          string  `json:"partial_file,omitempty"`
	InputSHA256      string  `json:"input_sha256,omitempty"`
	Duration         string  `json:"duration"`
	Error            string  `json:"error,omitempty"`
}

func (s *Summary) Finish(start time.Time, err error) {
	s.SchemaVersion = 1
	s.Success = err == nil
	s.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		s.Error = err.Error()
	}
}
func (s Summary) Print(w io.Writer) {
	fmt.Fprintf(w, "%s phase=%s success=%t validated=%d confirmed=%d unresolved=%d not_attempted=%d deleted=%d retries=%d elapsed=%s\n", s.Command, s.Phase, s.Success, s.Validated, s.Confirmed, s.Unresolved, s.NotAttempted, s.Deleted, s.Retries, s.Duration)
	if s.Recovery != "" {
		fmt.Fprintf(w, "Recovery: %s (complete=%t)\n", s.Recovery, s.RecoveryComplete)
	}
	if s.Partial != "" {
		fmt.Fprintf(w, "Partial output: %s\n", s.Partial)
	}
}
func (s Summary) Write(w io.Writer) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(s)
}
func NewFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
