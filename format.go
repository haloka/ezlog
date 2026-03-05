package ezlog

import (
	"errors"
	"fmt"
	"strings"
)

// FormatDetailed formats err into a human-readable multi-line string,
// traversing the full single-error cause chain from outermost to innermost.
//
// For each layer it prints the message, code (if set), and context fields.
// The stack trace is printed once, at the layer where it was captured.
//
// Safe to call multiple times; does not consume any internal state.
// Returns an empty string for nil errors.
//
// Example output:
//
//	request processing failed
//	  - request_id: abc-xyz
//	caused by: failed to find user
//	  code: USER_001
//	  - user_id: 123
//	caused by: record not found
//	Stack:
//	  github.com/example/app/repo.FetchUser
//	      repo/user.go:42
//	  github.com/example/app/api.Handler
//	      api/handler.go:20
func FormatDetailed(err error) string {
	if err == nil {
		return ""
	}
	var sb strings.Builder
	first := true
	for err != nil {
		if !first {
			sb.WriteString("caused by: ")
		}
		first = false

		// Use direct type assertion, NOT errors.As, to avoid traversing the
		// chain twice and accidentally processing the same layer more than once.
		if ez, ok := err.(*Error); ok {
			sb.WriteString(ez.msg)
			sb.WriteByte('\n')
			if ez.code != "" {
				fmt.Fprintf(&sb, "  code: %s\n", ez.code)
			}
			for _, f := range ez.fields {
				fmt.Fprintf(&sb, "  - %s: %v\n", f.Key, f.Value)
			}
			if len(ez.frames) > 0 {
				sb.WriteString("Stack:\n")
				for _, fr := range ez.frames {
					fmt.Fprintf(&sb, "  %s\n      %s:%d\n", fr.Function, fr.File, fr.Line)
				}
			}
		} else {
			sb.WriteString(err.Error())
			sb.WriteByte('\n')
		}

		err = errors.Unwrap(err)
	}
	return sb.String()
}
