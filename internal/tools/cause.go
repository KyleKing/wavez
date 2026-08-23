package tools

import (
	"errors"
	"io/fs"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/tool"
)

// causeOf classifies an error one of these tools is about to hand back, so
// a report can tell a safeguard working from a tool failing. The mapping
// lives in one place because the same sentinel means the same thing
// whichever tool returned it, and because a taxonomy spread across call
// sites drifts into a bucket per message.
//
// An error this does not recognize is CauseUnspecified rather than a guess:
// a wrong classification is worse than a gap, since a gap is visible in the
// report and a wrong one is not.
func causeOf(err error) tool.Cause {
	switch {
	case err == nil:
		return tool.CauseUnspecified

	case errors.Is(err, edit.ErrNotFound),
		errors.Is(err, ErrSymbolNotIndexed),
		errors.Is(err, ErrDeclarationMoved),
		errors.Is(err, fs.ErrNotExist):
		return tool.CauseNoMatch

	case errors.Is(err, ErrAmbiguousSymbol),
		errors.Is(err, edit.ErrNotUnique):
		return tool.CauseAmbiguous

	case errors.Is(err, edit.ErrEmptyOldString),
		errors.Is(err, edit.ErrNoChange),
		errors.Is(err, ErrPathMissing),
		errors.Is(err, errBadLineRange),
		errors.Is(err, ErrContextFiles):
		return tool.CauseBadInput

	// Every one of these is the tool declining work it could have done, so
	// they are the count that must not read as a defect.
	case errors.Is(err, ErrStillUsed),
		errors.Is(err, ErrCrossPackageMove),
		errors.Is(err, ErrNowhereToMove),
		errors.Is(err, ErrOutOfScope),
		errors.Is(err, ErrPathOutsideRoot),
		errors.Is(err, edit.ErrSymlink),
		errors.Is(err, errFetchDenied):
		return tool.CauseRefused

	case errors.Is(err, lease.ErrNoHolder),
		errors.Is(err, edit.ErrSpanOutOfRange):
		return tool.CauseConflict

	case errors.Is(err, ErrNoServer):
		return tool.CauseUpstream

	default:
		return tool.CauseUnspecified
	}
}

// failWith is tool.Fail with the cause read off the error, for the common
// case of handing back exactly what went wrong.
func failWith(err error) tool.Result {
	return tool.Fail(causeOf(err), "%v", err)
}
