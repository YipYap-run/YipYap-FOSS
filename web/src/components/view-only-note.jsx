import { currentUser } from '../state/auth';
import { viewOnlyNote } from '../state/permissions.mjs';

// ViewOnlyNote tells viewers why a page has no create, edit or delete
// controls. It renders nothing for roles that can write.
export function ViewOnlyNote() {
  const note = viewOnlyNote(currentUser.value?.role);
  if (!note) return null;
  return <p class="view-only-note">{note}</p>;
}
