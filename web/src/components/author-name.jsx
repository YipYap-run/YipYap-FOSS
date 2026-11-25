// DELETED_USER_LABEL matches domain.DeletedUserName on the server.
export const DELETED_USER_LABEL = 'Deleted user';

// AuthorName renders the author of a ticket message, incident update or
// similar record. A deleted account shows as "Deleted user" in muted italic
// so it reads as a placeholder, not as a person called that. The server
// sends the flag (user_deleted / sender_deleted) and an empty email for
// these rows, so the label never depends on parsing an address.
export function AuthorName({ deleted, name, fallback, style }) {
  if (deleted) {
    return (
      <span
        class="author-deleted"
        style={{ fontStyle: 'italic', color: 'var(--color-text-muted)', fontWeight: 600, ...style }}
        title="This account was deleted"
      >
        {name || DELETED_USER_LABEL}
      </span>
    );
  }
  return <span style={{ fontWeight: 600, ...style }}>{fallback}</span>;
}
