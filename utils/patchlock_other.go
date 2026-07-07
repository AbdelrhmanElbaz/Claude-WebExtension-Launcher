//go:build !windows

package utils

import "time"

// PatchLock is a no-op cross-process lock on non-Windows platforms. macOS patches
// in-process during a single launcher run, so there is nothing to serialize.
type PatchLock struct{}

// AcquirePatchLock always succeeds immediately on non-Windows platforms.
func AcquirePatchLock(name string, timeout time.Duration) (*PatchLock, bool) {
	return &PatchLock{}, true
}

// Release is a no-op.
func (l *PatchLock) Release() {}
