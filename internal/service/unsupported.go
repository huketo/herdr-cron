//go:build !linux && !darwin && !windows

package service

// New reports that this platform has no registration backend. The daemon and foreground
// drivers still work; only OS registration is unavailable.
func New() (Backend, error) { return nil, ErrUnsupported }
