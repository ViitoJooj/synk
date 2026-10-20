package folders

import (
	"errors"
	"os"

	"github.com/ViitoJooj/synk/pkg/errorx"
)

// Explain turns a refused change into what to tell the user, the same way
// for the command line and the control API.
func Explain(action string, err error) *errorx.Error {
	hint := ""
	switch {
	case errors.Is(err, ErrForbidden):
		hint = "keys, keyrings, browser profiles, synk's own files and the system are never synced; pick a folder that holds none of them"
	case errors.Is(err, ErrOverlap):
		hint = "a synced folder cannot be inside another, or hold one; see 'synk folder ls'"
	case errors.Is(err, ErrBlocked):
		hint = "it is already blocked; see 'synk block ls'"
	case errors.Is(err, ErrBuiltIn):
		hint = "built-in blocks cover system junk and secrets, and stay; see 'synk block ls'"
	case errors.Is(err, ErrExists):
		hint = "choose another name with --as, or see 'synk folder ls' and 'synk block ls'"
	case errors.Is(err, ErrBadName):
		hint = "name it with --as, for example --as codes"
	case errors.Is(err, ErrNotFound):
		hint = "see the names with 'synk folder ls' or 'synk block ls'"
	case errors.Is(err, ErrBlockedDir):
		hint = "take the block off with 'synk unblock', or pick another folder"
	case errors.Is(err, os.ErrNotExist):
		hint = "check the path"
	}
	return errorx.Folder(action, hint, err)
}
