package util

import "runtime"

// Platform represents the operating system platform, allowing tests to
// simulate different OS environments without being on that actual OS.
type Platform struct {
	OS string // "windows", "linux", "darwin"
}

// NewPlatform creates a Platform for the current runtime OS.
func NewPlatform() *Platform {
	return &Platform{OS: runtime.GOOS}
}

func (p *Platform) IsWindows() bool {
	return p.OS == "windows"
}

func (p *Platform) IsLinux() bool {
	return p.OS == "linux"
}

func (p *Platform) IsMac() bool {
	return p.OS == "darwin"
}

// Deprecated: Use Platform.IsWindows() instead. Kept for backward compatibility.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// Deprecated: Use Platform.IsLinux() instead. Kept for backward compatibility.
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

// Deprecated: Use Platform.IsMac() instead. Kept for backward compatibility.
func IsMac() bool {
	return runtime.GOOS == "darwin"
}
