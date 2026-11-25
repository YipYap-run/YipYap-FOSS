import { useState, useEffect, useRef } from 'preact/hooks';
import { get } from '../../api/client';
import { usersQuery, userPage, userLabel, pickerOptions, PICKER_LIMIT } from './members-view.mjs';

// UserPicker finds a member by searching GET /users instead of relying on the
// first page of the member list (ux-3). It is a search box plus a native
// select of the matches, so it works with a keyboard and screen readers
// without a custom combobox. `role` limits matches (Transfer Ownership uses
// admin); `exclude` hides IDs already chosen (current team members). The
// chosen person stays in the list while the search changes, and a `value`
// set by the caller is looked up by ID so the select never shows a raw ID.
export function UserPicker({ id, label, role = '', exclude = [], value, onChange, required = false, invalid = false, describedBy }) {
  const [q, setQ] = useState('');
  const [matches, setMatches] = useState([]);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState('loading');
  const [selected, setSelected] = useState(null);
  const [attempt, setAttempt] = useState(0);
  const seq = useRef(0);

  // Resolve a value the list does not hold (set by the caller, or chosen
  // before the search changed) with one ID lookup.
  useEffect(() => {
    if (!value) { setSelected(null); return; }
    if (selected?.id === value) return;
    const hit = matches.find(u => u.id === value);
    if (hit) { setSelected(hit); return; }
    let live = true;
    get(usersQuery({ ids: [value] }))
      .then(data => { if (live) setSelected(userPage(data).users.find(u => u.id === value) || null); })
      .catch(() => {});
    return () => { live = false; };
  }, [value]);

  useEffect(() => {
    const mine = ++seq.current;
    setStatus('loading');
    const t = setTimeout(async () => {
      try {
        const data = await get(usersQuery({ q, role, limit: PICKER_LIMIT }));
        if (mine !== seq.current) return;
        const page = userPage(data);
        setMatches(page.users);
        setTotal(page.total);
        setStatus('ready');
      } catch (_) {
        if (mine !== seq.current) return;
        setMatches([]);
        setTotal(0);
        setStatus('error');
      }
    }, q ? 250 : 0);
    return () => clearTimeout(t);
  }, [q, role, attempt]);

  const shown = pickerOptions(matches, exclude, selected?.id === value ? selected : null);
  // found counts search matches only, not the kept choice.
  const found = pickerOptions(matches, exclude);

  function choose(id) {
    setSelected(shown.find(u => u.id === id) || null);
    onChange(id);
  }

  let note = '';
  if (status === 'loading') note = 'Searching...';
  else if (status === 'error') note = 'Could not search members.';
  else if (found.length === 0) note = q ? 'No matching members.' : 'No members to choose from.';
  else if (total > matches.length) note = `Showing ${found.length} of ${total}. Type to narrow the list.`;

  const searchId = `${id}-search`;
  const noteId = `${id}-note`;
  return (
    <div class="form-group">
      <label for={searchId}>Search {label.toLowerCase()}</label>
      <input id={searchId} type="search" value={q} autocomplete="off" placeholder="Email or name"
             onInput={e => setQ(e.target.value)} aria-describedby={noteId} />
      <label for={id} style="margin-top: 8px;">{label}</label>
      <select id={id} value={value} required={required} onChange={e => choose(e.target.value)}
              aria-invalid={invalid ? 'true' : undefined}
              aria-describedby={[noteId, describedBy].filter(Boolean).join(' ')}>
        <option value="">Select...</option>
        {shown.map(u => <option key={u.id} value={u.id}>{userLabel(u)}</option>)}
      </select>
      <p id={noteId} class="form-help" aria-live="polite">
        {note}
        {status === 'error' && (
          <> <button type="button" class="btn btn-xs" onClick={() => setAttempt(n => n + 1)}>Retry</button></>
        )}
      </p>
    </div>
  );
}
