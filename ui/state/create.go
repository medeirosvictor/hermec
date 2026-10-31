package state

import (
	"strconv"
	"strings"
)

const (
	// PortRangeMsg and NameRequiredMsg are the user-facing reasons ValidateCreate rejects a form.
	PortRangeMsg    = "port must be 1–65535"
	NameRequiredMsg = "server name is required"
)

// ValidateCreate checks the create-server form text: a non-blank name and a
// port in 1–65535. err is the user-facing reason it cannot submit.
func ValidateCreate(nameText, portText string) (name string, port int, err string) {
	name = strings.TrimSpace(nameText)
	if name == "" {
		return "", 0, NameRequiredMsg
	}
	port, e := strconv.Atoi(portText)
	if e != nil || port < 1 || port > 65535 {
		return "", 0, PortRangeMsg
	}
	return name, port, ""
}
