import { useState, useEffect, useRef } from 'preact/hooks';
import {
  filterOptions,
  nextActive,
  selectedLabel,
  optionId,
  resultsMessage,
  escapeAction,
} from './combobox-state.mjs';
import './combobox.css';

/* Searchable picker: an editable combobox with a listbox popup (WAI-ARIA
   1.2 pattern, list autocomplete, no automatic selection).

   Keyboard: type to filter; Down/Up open the list and move through it
   (Alt+Down opens without moving); PageDown/PageUp jump 10; Enter picks the
   highlighted option (or the only match); Escape closes the list, and a
   second Escape clears the search; Tab leaves without changing the choice.

   Props:
     id           input id; the caller's <label for={id}> names it
     labelId      id of that <label>, used to name the listbox
     options      [{ value, label }]
     value        the chosen value ('' for none)
     onSelect     called with the chosen value
     disabled, busy, required, invalid, describedBy, placeholder
     noun         singular word for counts, e.g. 'channel' */
export function ChannelCombobox({
  id, labelId, options, value, onSelect, disabled, busy, required, invalid,
  describedBy, placeholder, noun = 'channel',
}) {
  const [query, setQuery] = useState(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const inputRef = useRef(null);
  const listId = id + '-listbox';
  const statusId = id + '-status';

  const shown = open ? filterOptions(options, query || '') : [];
  const text = query !== null ? query : selectedLabel(options, value);

  // Keep the highlighted option visible while moving with the keyboard.
  useEffect(() => {
    if (!open || active < 0 || !shown[active]) return;
    const el = typeof document !== 'undefined' && document.getElementById(optionId(id, shown[active].value, active));
    if (el && el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
  }, [open, active]);

  // Closing on disable (for example while the list reloads).
  useEffect(() => { if (disabled) close(); }, [disabled]);

  function close() {
    setOpen(false);
    setActive(-1);
    setQuery(null);
  }

  function choose(o) {
    if (!o) return;
    onSelect(o.value);
    close();
  }

  function openList(key) {
    const list = filterOptions(options, query || '');
    setOpen(true);
    if (key === 'ArrowDown' || key === 'ArrowUp') {
      const at = list.findIndex(o => o.value === value);
      setActive(at >= 0 ? at : nextActive(-1, list.length, key));
    }
  }

  function onKeyDown(e) {
    if (disabled) return;
    const key = e.key;
    if (key === 'ArrowDown' && e.altKey) {
      e.preventDefault();
      if (!open) openList(null);
      return;
    }
    if (key === 'ArrowDown' || key === 'ArrowUp' || key === 'PageDown' || key === 'PageUp') {
      e.preventDefault();
      if (!open) { openList(key); return; }
      const n = nextActive(active, shown.length, key);
      if (n !== null) setActive(n);
      return;
    }
    if (key === 'Enter') {
      if (!open) return;
      // Never submit the surrounding form while the list is open.
      e.preventDefault();
      if (active >= 0) choose(shown[active]);
      else if (shown.length === 1) choose(shown[0]);
      return;
    }
    if (key === 'Escape') {
      const act = escapeAction(open, query);
      if (!act) return;
      // Keep the dialog around the form open.
      e.stopPropagation();
      e.preventDefault();
      if (act === 'close') {
        setOpen(false);
        setActive(-1);
      } else {
        setQuery(null);
      }
      return;
    }
    if (key === 'Tab') close();
  }

  const activeId = open && active >= 0 && shown[active] ? optionId(id, shown[active].value, active) : undefined;

  return (
    <div class={'combobox' + (open ? ' is-open' : '')}>
      <div class="combobox-field">
        <input ref={inputRef} id={id} type="text" role="combobox"
          class="combobox-input"
          value={text}
          placeholder={placeholder}
          disabled={disabled}
          autocomplete="off" autocapitalize="off" spellcheck={false}
          aria-autocomplete="list"
          aria-expanded={open ? 'true' : 'false'}
          aria-controls={listId}
          aria-activedescendant={activeId}
          aria-busy={busy ? 'true' : undefined}
          aria-required={required ? 'true' : undefined}
          aria-invalid={invalid ? 'true' : undefined}
          aria-describedby={describedBy}
          onInput={e => { setQuery(e.currentTarget.value); setOpen(true); setActive(-1); }}
          onClick={() => { if (!open && !disabled) openList(null); }}
          onKeyDown={onKeyDown}
          onBlur={close} />
        {/* Pointer shortcut only. Keyboard users open the list with Down,
            so the button stays out of the tab order (APG pattern). */}
        <button type="button" class="combobox-toggle" tabIndex={-1}
          aria-label={(open ? 'Hide ' : 'Show ') + noun + 's'}
          aria-expanded={open ? 'true' : 'false'}
          aria-controls={listId}
          disabled={disabled}
          onMouseDown={e => e.preventDefault()}
          onClick={() => {
            if (open) close();
            else { openList(null); if (inputRef.current) inputRef.current.focus(); }
          }}>
          <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false">
            <path d="M3 6l5 5 5-5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
      <ul id={listId} role="listbox" class="combobox-list"
        aria-labelledby={labelId} hidden={!open || shown.length === 0}
        onMouseDown={e => e.preventDefault()}>
        {shown.map((o, i) => (
          <li key={o.value} id={optionId(id, o.value, i)} role="option"
            class={'combobox-option' + (i === active ? ' is-active' : '')}
            aria-selected={o.value === value ? 'true' : 'false'}
            onMouseMove={() => { if (active !== i) setActive(i); }}
            onClick={() => choose(o)}>
            {o.label}
          </li>
        ))}
      </ul>
      {open && shown.length === 0 && (
        <div class="combobox-empty">{resultsMessage(0, query, noun)}</div>
      )}
      <div id={statusId} class="combobox-sr" role="status" aria-live="polite">
        {open ? resultsMessage(shown.length, query, noun) : ''}
      </div>
    </div>
  );
}
