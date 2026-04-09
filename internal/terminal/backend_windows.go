//go:build windows

package terminal

// currentTTYPlatform always returns "" on Windows.
func currentTTYPlatform() string {
	return ""
}
