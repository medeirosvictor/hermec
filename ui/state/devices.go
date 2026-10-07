package state

import "fmt"

// ResolveDevice decides which device to open for a saved name. kind is
// "input" or "output" (for the notice). An empty want means the system
// default. A saved name that is no longer present falls back to the default
// ("") with a notice; it is never an error.
func ResolveDevice(kind, want string, available []string) (use, notice string) {
	if want == "" {
		return "", ""
	}
	for _, d := range available {
		if d == want {
			return want, ""
		}
	}
	return "", fmt.Sprintf("%s device %q not found; using default", kind, want)
}

// NextDevice cycles default -> available[0] -> available[1] ... -> default.
// A current value that is not in the list counts as default.
func NextDevice(cur string, available []string) string {
	next := 0 // index into available of the next pick
	if cur != "" {
		for i, d := range available {
			if d == cur {
				next = i + 1
				break
			}
		}
	}
	if next >= len(available) {
		return ""
	}
	return available[next]
}
