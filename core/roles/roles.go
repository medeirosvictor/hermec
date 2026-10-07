package roles

type Permission string

const (
	PermJoinChannel Permission = "join_channel"
	PermSendChat    Permission = "send_chat"
	PermManage      Permission = "manage"
)

type Config struct {
	Roles        map[string][]Permission
	Grants       map[string][]string
	DefaultRoles []string
}

// Builtin returns the builtin role definitions.
func Builtin() map[string][]Permission {
	return map[string][]Permission{
		"admin": {PermJoinChannel, PermSendChat, PermManage},
		"user":  {PermJoinChannel, PermSendChat},
		"bot":   {PermJoinChannel, PermSendChat},
	}
}

// RolesFor returns the list of roles assigned to the given fingerprint.
// If the fingerprint is not in Grants, returns DefaultRoles.
func (c Config) RolesFor(fingerprint string) []string {
	if roles, ok := c.Grants[fingerprint]; ok {
		return roles
	}
	return c.DefaultRoles
}

// Has returns true if the given fingerprint has the given permission.
// It looks up the roles assigned to the fingerprint, then checks if any of
// those roles have the permission. Unknown role names are ignored.
func (c Config) Has(fingerprint string, p Permission) bool {
	roleNames := c.RolesFor(fingerprint)
	for _, roleName := range roleNames {
		if perms, ok := c.Roles[roleName]; ok {
			for _, perm := range perms {
				if perm == p {
					return true
				}
			}
		}
	}
	return false
}
