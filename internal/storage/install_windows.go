//go:build windows

package storage

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func syncDirectory(string) error { return nil }

func isTargetExists(err error) bool {
	return errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, syscall.ERROR_FILE_EXISTS)
}

func isLinkUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}

func isRenameUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}

func noReplaceRename(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, 0)
}

var _ = os.ErrExist
