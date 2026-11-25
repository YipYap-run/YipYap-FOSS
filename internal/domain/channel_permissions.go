package domain

import (
	"fmt"
	"strings"
)

// Org setting keys that hold which roles may act on notification channels.
// Values are comma-separated role names.
const (
	SettingChannelManageRoles = "channel_manage_roles"
	SettingChannelTestRoles   = "channel_test_roles"
)

// ChannelAction is an operation gated by the org's channel permissions.
type ChannelAction string

const (
	// ChannelActionManage covers create, update and delete.
	ChannelActionManage ChannelAction = "manage"
	// ChannelActionTest covers sending a test notification.
	ChannelActionTest ChannelAction = "test"
)

// channelRoleOrder is the canonical order roles are stored and returned in.
var channelRoleOrder = []UserRole{RoleOwner, RoleAdmin, RoleMember, RoleViewer}

// ChannelPermissions lists the user roles allowed to manage and to test
// notification channels in one org.
type ChannelPermissions struct {
	ManageRoles []string `json:"manage_roles"`
	TestRoles   []string `json:"test_roles"`
}

// DefaultChannelPermissions is what an org gets until an admin changes it:
// owners and admins manage, members may also send a test.
func DefaultChannelPermissions() ChannelPermissions {
	return ChannelPermissions{
		ManageRoles: []string{string(RoleOwner), string(RoleAdmin)},
		TestRoles:   []string{string(RoleOwner), string(RoleAdmin), string(RoleMember)},
	}
}

// NormalizeChannelPermissions validates role names and returns the
// canonical form: the owner is always in both lists, anyone who may manage
// may also test, viewers may never manage, and roles are deduplicated and
// ordered owner, admin, member, viewer.
func NormalizeChannelPermissions(p ChannelPermissions) (ChannelPermissions, error) {
	manage, err := roleSet(p.ManageRoles)
	if err != nil {
		return ChannelPermissions{}, fmt.Errorf("manage_roles: %w", err)
	}
	test, err := roleSet(p.TestRoles)
	if err != nil {
		return ChannelPermissions{}, fmt.Errorf("test_roles: %w", err)
	}
	if manage[RoleViewer] {
		return ChannelPermissions{}, fmt.Errorf("manage_roles: viewers cannot manage channels")
	}
	manage[RoleOwner] = true
	test[RoleOwner] = true
	for r := range manage {
		test[r] = true
	}
	return ChannelPermissions{ManageRoles: orderedRoles(manage), TestRoles: orderedRoles(test)}, nil
}

// Allows reports whether role may perform action.
func (p ChannelPermissions) Allows(role string, action ChannelAction) bool {
	if role == string(RoleOwner) {
		return true
	}
	var list []string
	switch action {
	case ChannelActionManage:
		list = p.ManageRoles
	case ChannelActionTest:
		list = p.TestRoles
	default:
		return false
	}
	for _, r := range list {
		if r == role {
			return true
		}
	}
	return false
}

// ChannelPermissionsFromSettings builds the effective permissions from an
// org's settings map. A missing or invalid value falls back to the default
// for that list, so a bad row can never lock the owner out or widen access.
func ChannelPermissionsFromSettings(settings map[string]string) ChannelPermissions {
	def := DefaultChannelPermissions()
	p := def
	if v, ok := settings[SettingChannelManageRoles]; ok {
		if _, err := NormalizeChannelPermissions(ChannelPermissions{ManageRoles: SplitRoles(v)}); err == nil {
			p.ManageRoles = SplitRoles(v)
		}
	}
	if v, ok := settings[SettingChannelTestRoles]; ok {
		if _, err := roleSet(SplitRoles(v)); err == nil {
			p.TestRoles = SplitRoles(v)
		}
	}
	// Each list is valid on its own here, so this cannot fail.
	n, _ := NormalizeChannelPermissions(p)
	return n
}

// SplitRoles parses a comma-separated role list as stored in org settings.
func SplitRoles(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func roleSet(roles []string) (map[UserRole]bool, error) {
	set := make(map[UserRole]bool, len(roles))
	for _, r := range roles {
		role := UserRole(strings.ToLower(strings.TrimSpace(r)))
		switch role {
		case RoleOwner, RoleAdmin, RoleMember, RoleViewer:
			set[role] = true
		default:
			return nil, fmt.Errorf("unknown role %q", r)
		}
	}
	return set, nil
}

func orderedRoles(set map[UserRole]bool) []string {
	out := make([]string, 0, len(set))
	for _, r := range channelRoleOrder {
		if set[r] {
			out = append(out, string(r))
		}
	}
	return out
}
