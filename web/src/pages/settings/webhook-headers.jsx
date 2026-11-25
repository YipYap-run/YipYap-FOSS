import { useState } from 'preact/hooks';
import { newHeaderRow, headersSummary, isBlankRow, MAX_HEADERS } from './webhook-headers-logic.mjs';
import './webhook-headers.css';

// Row editor for webhook custom headers (notif-7). Saved values are
// write-only: the row shows the name and "Value set" with Replace and
// Remove. A saved name cannot be edited because the stored value belongs
// to it; to rename, remove the row and add a new one.
//
// rows: editor rows (webhook-headers-logic.mjs). rowErrors: { [row.key]:
// { name?, value? } }. onChange(rows, changedKey) reports every edit.
export function WebhookHeadersEditor({ id, rows, rowErrors = {}, invalid, describedBy, onChange }) {
  const list = Array.isArray(rows) ? rows : [];
  const [shown, setShown] = useState({});
  const [announce, setAnnounce] = useState('');
  const atCap = list.filter(r => !isBlankRow(r)).length >= MAX_HEADERS;

  const focusLater = (elId) => setTimeout(() => document.getElementById(elId)?.focus(), 0);
  const rid = (row, part) => id + '-' + row.key + '-' + part;

  const update = (row, patch) => onChange(list.map(r => (r.key === row.key ? { ...r, ...patch } : r)), row.key);

  const add = () => {
    const row = newHeaderRow();
    onChange([...list, row], row.key);
    setAnnounce('');
    focusLater(rid(row, 'name'));
  };

  const remove = (row, index) => {
    const next = list.filter(r => r.key !== row.key);
    onChange(next, row.key);
    setAnnounce((row.name.trim() || 'Header') + ' removed. Save to apply.');
    const after = next[index] || next[index - 1];
    focusLater(after ? rid(after, 'remove') : id + '-add');
  };

  const replace = (row) => {
    update(row, { replacing: true, value: '' });
    focusLater(rid(row, 'value'));
  };

  const keepSaved = (row) => {
    update(row, { replacing: false, value: '' });
    setShown(s => ({ ...s, [row.key]: false }));
    focusLater(rid(row, 'replace'));
  };

  return (
    <div class="headers-editor" id={id} role="group" tabIndex={-1}
      aria-labelledby={id + '-label'} aria-describedby={describedBy}
      aria-invalid={invalid ? 'true' : undefined}>
      {list.length === 0 ? (
        <p class="headers-empty">No custom headers. Requests carry only the headers YipYap sets.</p>
      ) : (
        <ul class="headers-rows" aria-label={headersSummary(list)}>
          {list.map((row, i) => {
            const err = rowErrors[row.key] || {};
            const label = row.name.trim() || 'header ' + (i + 1);
            const nameErrId = rid(row, 'name-error');
            const valueErrId = rid(row, 'value-error');
            const editingValue = !row.saved || row.replacing;
            return (
              <li key={row.key} class={'headers-row' + (err.name || err.value ? ' has-error' : '')}>
                <div class="headers-cell headers-name">
                  {row.saved ? (
                    <>
                      <span class="headers-cell-label" aria-hidden="true">Name</span>
                      <code class="headers-saved-name" id={rid(row, 'name')}>{row.name}</code>
                    </>
                  ) : (
                    <>
                      <label class="headers-cell-label" for={rid(row, 'name')}>Name</label>
                      <input id={rid(row, 'name')} type="text" value={row.name} placeholder="X-Api-Key"
                        autocomplete="off" autocapitalize="off" spellcheck={false} maxLength={256}
                        aria-invalid={err.name ? 'true' : undefined} aria-describedby={err.name ? nameErrId : undefined}
                        onInput={e => update(row, { name: e.target.value })} />
                    </>
                  )}
                  {err.name && <div class="field-error" id={nameErrId}>{err.name}</div>}
                </div>
                <div class="headers-cell headers-value">
                  {editingValue ? (
                    <>
                      <label class="headers-cell-label" for={rid(row, 'value')}>
                        {row.replacing ? 'New value' : 'Value'}
                        <span class="headers-sr"> for {label}</span>
                      </label>
                      <div class="headers-value-input">
                        <input id={rid(row, 'value')} type={shown[row.key] ? 'text' : 'password'} value={row.value}
                          autocomplete="off" autocapitalize="off" spellcheck={false} maxLength={4096}
                          aria-invalid={err.value ? 'true' : undefined} aria-describedby={err.value ? valueErrId : undefined}
                          onInput={e => update(row, { value: e.target.value })} />
                        <button type="button" class="btn btn-xs" aria-pressed={shown[row.key] ? 'true' : 'false'}
                          aria-label={(shown[row.key] ? 'Hide' : 'Show') + ' value for ' + label}
                          onClick={() => setShown(s => ({ ...s, [row.key]: !s[row.key] }))}>
                          {shown[row.key] ? 'Hide' : 'Show'}
                        </button>
                      </div>
                    </>
                  ) : (
                    <>
                      <span class="headers-cell-label" aria-hidden="true">Value</span>
                      <span class="headers-value-set">Value set</span>
                    </>
                  )}
                  {err.value && <div class="field-error" id={valueErrId}>{err.value}</div>}
                </div>
                <div class="headers-cell headers-actions">
                  {row.saved && !row.replacing && (
                    <button type="button" class="btn btn-xs" id={rid(row, 'replace')}
                      aria-label={'Replace value for ' + label} onClick={() => replace(row)}>Replace</button>
                  )}
                  {row.saved && row.replacing && (
                    <button type="button" class="btn btn-xs" aria-label={'Keep saved value for ' + label}
                      onClick={() => keepSaved(row)}>Keep saved</button>
                  )}
                  <button type="button" class="btn btn-xs" id={rid(row, 'remove')}
                    aria-label={'Remove ' + label} onClick={() => remove(row, i)}>Remove</button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      <button type="button" class="btn btn-sm headers-add" id={id + '-add'} onClick={add} disabled={atCap}>
        Add header
      </button>
      {atCap && <span class="form-help headers-cap">You have reached the limit of {MAX_HEADERS} headers.</span>}
      <div class="headers-sr" role="status" aria-live="polite">{announce}</div>
    </div>
  );
}
