package domain

import "strings"

// DeletedUserName is the author label shown for records whose user was
// deleted.
const DeletedUserName = "Deleted user"

const (
	deletedEmailPrefix = "deleted-"
	deletedEmailSuffix = "@invalid"
)

// DeletedUserEmail is the placeholder address a deleted user's row keeps.
// The .invalid TLD never resolves (RFC 2606) and ValidateEmail rejects a
// host without a dot, so no real account can sign up with it.
func DeletedUserEmail(userID string) string {
	return deletedEmailPrefix + userID + deletedEmailSuffix
}

// IsDeletedUserEmail reports whether email is a deleted user's placeholder.
func IsDeletedUserEmail(email string) bool {
	return strings.HasPrefix(email, deletedEmailPrefix) && strings.HasSuffix(email, deletedEmailSuffix)
}
