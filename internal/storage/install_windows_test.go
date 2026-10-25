//go:build windows

package storage

import "golang.org/x/sys/windows"

// Platform error values used by the install classification tests.
var (
	errLinkUnsupported   error = windows.ERROR_NOT_SUPPORTED
	errTargetExists      error = windows.ERROR_FILE_EXISTS
	errOtherInstall      error = windows.ERROR_ACCESS_DENIED
	errRenameUnsupported error = windows.ERROR_INVALID_FUNCTION
)
