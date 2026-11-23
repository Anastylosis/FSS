package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/scraper"
)

// jsonFormatVersion versions the --json envelope and result shapes. It is a
// separate line from models.StoreSchemaVersion. Bump it only for changes that
// break an existing reader, not for added fields.
const jsonFormatVersion = 1

// Error codes in a --json error object. Kept coarse on purpose: each one
// should map to a different next step for the caller.
const (
	codeInvalidArgs      = "invalid_args"
	codeConfig           = "config"
	codeUnsupportedURL   = "unsupported_url"
	codeTransport        = "transport"
	codeParse            = "parse"
	codeRefused          = "refused"
	codeInterrupted      = "interrupted"
	codeStashUnreachable = "stash_unreachable"
	codeError            = "error"
)

type jsonEnvelope struct {
	FormatVersion int        `json:"formatVersion"`
	Command       string     `json:"command"`
	Type          string     `json:"type,omitempty"`
	OK            bool       `json:"ok"`
	Result        any        `json:"result"`
	Error         *jsonError `json:"error"`
}

type jsonError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Reason     string `json:"reason,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
}

// codedError pins a --json error code on an error at the point where the
// cause is known, instead of guessing it from the message later.
type codedError struct {
	code, reason string
	err          error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func withCode(code string, err error) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, err: err}
}

func withReason(code, reason string, err error) error {
	return &codedError{code: code, reason: reason, err: err}
}

// The output switch is --format, not --json: --json already names input
// files on the commands that share scenesource.go.
func addFormatFlag(c *cobra.Command) {
	c.Flags().String("format", "text", "output format: text, or json for a machine-readable result on stdout (see docs/json-output.md)")
}

func jsonMode(c *cobra.Command) bool {
	if c == nil || c.Flags().Lookup("format") == nil {
		return false
	}
	f, _ := c.Flags().GetString("format")
	return f == "json"
}

func checkFormatFlag(c *cobra.Command) error {
	if c.Flags().Lookup("format") == nil {
		return nil
	}
	switch f, _ := c.Flags().GetString("format"); f {
	case "text", "json":
		return nil
	default:
		return withCode(codeInvalidArgs, fmt.Errorf("--format must be text or json, not %q", f))
	}
}

func toJSONError(err error) *jsonError {
	if err == nil {
		return nil
	}
	je := &jsonError{Code: codeError, Message: err.Error()}
	var ce *codedError
	var se *httpx.StatusError
	switch {
	case errors.As(err, &ce):
		je.Code, je.Reason = ce.code, ce.reason
	case exitCodeOf(err) == ExitInterrupted:
		je.Code = codeInterrupted
	case exitCodeOf(err) == ExitRefused:
		je.Code = codeRefused
	default:
		switch scraper.Classify(err) {
		case scraper.FailureTransport:
			je.Code = codeTransport
		case scraper.FailureParse:
			je.Code = codeParse
		}
	}
	if errors.As(err, &se) {
		je.HTTPStatus = se.StatusCode
	}
	return je
}

// writePreRunEnvelope reports an error raised before the command ran.
func writePreRunEnvelope(w io.Writer, command string, err error) {
	writeEnvelope(w, command, nil, err)
}

func writeEnvelope(w io.Writer, command string, result any, err error) {
	writeEnvelopeTyped(w, command, "", result, err)
}

func writeEnvelopeTyped(w io.Writer, command, typ string, result any, err error) {
	env := jsonEnvelope{
		FormatVersion: jsonFormatVersion,
		Type:          typ,
		Command:       command,
		OK:            err == nil,
		Result:        result,
		Error:         toJSONError(err),
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if eErr := enc.Encode(env); eErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "writing JSON: %v\n", eErr)
	}
}

// emitJSON prints the envelope and hands back err marked as already
// reported, so Execute keeps the exit code but prints nothing more.
func emitJSON(c *cobra.Command, result any, err error) error {
	writeEnvelope(c.OutOrStdout(), commandName(c), result, err)
	if err == nil {
		return nil
	}
	_, _ = fmt.Fprintf(c.ErrOrStderr(), "Error: %v\n", err)
	return &exitError{code: exitCodeOf(err), err: err, reported: true}
}

// commandName is the command path without the binary name: "stash import".
func commandName(c *cobra.Command) string {
	return strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
}

// jsonFromArgs pre-scans argv like langFromArgs, so that errors raised before
// a command runs (bad flags, unreadable config) still come out as JSON.
func jsonFromArgs(args []string) bool {
	for i, a := range args {
		if a == "--" {
			return false
		}
		if a == "--format=json" || (a == "--format" && i+1 < len(args) && args[i+1] == "json") {
			return true
		}
	}
	return false
}

// errorCommandName finds the subcommand argv was aimed at, for the envelope of
// an error that happened before it ran.
func errorCommandName(root *cobra.Command, args []string) string {
	if c, _, err := root.Find(args); err == nil && c != root {
		return commandName(c)
	}
	return ""
}

// classifyPreRun tags errors from flag parsing and config loading, which never
// reach a command's own handler.
func classifyPreRun(err error) error {
	var ce *codedError
	if errors.As(err, &ce) {
		return err
	}
	msg := err.Error()
	for _, p := range []string{"unknown flag", "unknown shorthand", "unknown command", "flag needs an argument", "invalid argument", "accepts ", "requires at least", "requires at most", "required flag", "if any flags in the group"} {
		if strings.Contains(msg, p) {
			return withCode(codeInvalidArgs, err)
		}
	}
	return err
}
