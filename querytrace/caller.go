package querytrace

import (
	"runtime"
	"strings"
	"time"
)

// CaptureCaller walks the stack and returns the first frame outside querytrace
// and the database/sql machinery — the application code that issued the
// statement. It returns nil when no such frame is found. It is part of the
// instrumentation contract used by wrapped drivers; call it from the driver
// method that runs the statement so the application frame is reached.
func CaptureCaller() *Caller {
	const depth = 32
	pcs := make([]uintptr, depth)
	// Skip runtime.Callers and captureCaller itself.
	n := runtime.Callers(2, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.Function != "" && !isInternalFrame(frame.Function) {
			return &Caller{
				Function: frame.Function,
				File:     frame.File,
				Line:     frame.Line,
			}
		}
		if !more {
			break
		}
	}
	return nil
}

// isInternalFrame reports whether fn belongs to querytrace or the standard
// database/sql layer, frames the application never wants attributed to itself.
func isInternalFrame(fn string) bool {
	switch {
	case strings.Contains(fn, "github.com/aita/sqlkit/querytrace"):
		return true
	case strings.HasPrefix(fn, "database/sql"):
		return true
	default:
		return false
	}
}

// WantCaller reports whether a statement with the given outcome should have its
// caller captured under mode. It is part of the instrumentation contract used by
// wrapped drivers.
func WantCaller(mode CallerMode, err error, dur, slow time.Duration) bool {
	switch mode {
	case CallerAlways:
		return true
	case CallerOnSlowQuery:
		return err != nil || (slow > 0 && dur >= slow)
	case CallerOnError:
		return err != nil
	default:
		return false
	}
}
