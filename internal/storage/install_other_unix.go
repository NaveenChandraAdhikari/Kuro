//go:build !windows && !linux && !darwin

package storage

import "errors"

func noReplaceRename(_, _ string) error {
	return errors.New("no-replace rename is unsupported on this platform")
}
