package ui

// openCommand is the program that opens a URL or folder on goos: open on
// macOS, xdg-open on Linux and the other Unixes. Windows has its own openURL.
func openCommand(goos, target string) (name string, args []string) {
	if goos == "darwin" {
		return "open", []string{target}
	}
	return "xdg-open", []string{target}
}
