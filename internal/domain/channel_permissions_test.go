package domain

import (
	"reflect"
	"testing"
)

func TestNormalizeChannelPermissions(t *testing.T) {
	got, err := NormalizeChannelPermissions(ChannelPermissions{
		ManageRoles: []string{"member", " Admin ", "member"},
		TestRoles:   []string{"viewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ChannelPermissions{
		ManageRoles: []string{"owner", "admin", "member"},
		TestRoles:   []string{"owner", "admin", "member", "viewer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, bad := range []ChannelPermissions{
		{ManageRoles: []string{"viewer"}},
		{ManageRoles: []string{"api_key"}},
		{TestRoles: []string{"root"}},
	} {
		if _, err := NormalizeChannelPermissions(bad); err == nil {
			t.Errorf("%+v: expected error", bad)
		}
	}
}

func TestChannelPermissionsFromSettings(t *testing.T) {
	def := DefaultChannelPermissions()
	if got := ChannelPermissionsFromSettings(nil); !reflect.DeepEqual(got, def) {
		t.Fatalf("empty settings: got %+v, want defaults", got)
	}
	// A corrupt manage list falls back to the default without widening
	// access; the valid test list is kept.
	got := ChannelPermissionsFromSettings(map[string]string{
		SettingChannelManageRoles: "viewer,member",
		SettingChannelTestRoles:   "viewer",
	})
	if !reflect.DeepEqual(got.ManageRoles, def.ManageRoles) {
		t.Errorf("manage = %v, want default", got.ManageRoles)
	}
	if want := []string{"owner", "admin", "viewer"}; !reflect.DeepEqual(got.TestRoles, want) {
		t.Errorf("test roles = %v, want %v", got.TestRoles, want)
	}
	// An empty stored list still lets the owner in.
	got = ChannelPermissionsFromSettings(map[string]string{SettingChannelManageRoles: "", SettingChannelTestRoles: ""})
	if !got.Allows("owner", ChannelActionManage) || got.Allows("admin", ChannelActionManage) || got.Allows("member", ChannelActionTest) {
		t.Errorf("empty lists: %+v", got)
	}
	if !got.Allows("owner", "bogus") {
		t.Errorf("owner should pass any action")
	}
	if def.Allows("admin", "bogus") {
		t.Errorf("unknown action must be denied")
	}
}
