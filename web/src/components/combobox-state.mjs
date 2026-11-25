/* Pure helpers for ChannelCombobox (combobox.jsx). Kept free of Preact so
   `npm test` can check them. The widget follows the WAI-ARIA 1.2 "combobox
   with listbox popup, list autocomplete" pattern: typing filters the list,
   nothing is chosen until the user presses Enter or clicks an option.

   Options are { value, label } as returned by channelOptions() in
   slack-app-channel-state.mjs and discord-app-state.mjs. */

/* Lower case, with a leading '#' and surrounding space removed, so "#ops",
   "ops" and " Ops" all match the channel "#ops-alerts". */
export function normalizeQuery(q) {
  return String(q || '').trim().replace(/^#+/, '').toLowerCase();
}

/* Options whose label contains the query. Labels that start with the
   query come first, each group keeping its original order. */
export function filterOptions(options, query) {
  const list = Array.isArray(options) ? options : [];
  const q = normalizeQuery(query);
  if (!q) return list.slice();
  const starts = [];
  const contains = [];
  for (const o of list) {
    const label = normalizeQuery(o && o.label);
    const at = label.indexOf(q);
    if (at === 0) starts.push(o);
    else if (at > 0) contains.push(o);
  }
  return starts.concat(contains);
}

/* The next active index for a navigation key, or null when the key is not
   a list navigation key. -1 means no active option. ArrowDown and ArrowUp
   wrap; PageDown and PageUp jump 10 without wrapping. */
export function nextActive(index, count, key) {
  if (count <= 0) return key === 'ArrowDown' || key === 'ArrowUp' ? -1 : null;
  const i = typeof index === 'number' ? index : -1;
  switch (key) {
    case 'ArrowDown': return i < 0 || i >= count - 1 ? 0 : i + 1;
    case 'ArrowUp': return i <= 0 ? count - 1 : i - 1;
    case 'PageDown': return Math.min(count - 1, Math.max(0, i) + 10);
    case 'PageUp': return Math.max(0, i - 10);
    default: return null;
  }
}

/* The label of the chosen option, or '' when nothing is chosen. */
export function selectedLabel(options, value) {
  if (!value) return '';
  const o = (Array.isArray(options) ? options : []).find(x => x && x.value === value);
  return o ? o.label : '';
}

/* A DOM id for an option. Channel ids are Slack or Discord ids
   (letters and digits), but anything outside [A-Za-z0-9_-] is replaced so a
   crafted value cannot break the id reference. */
export function optionId(baseId, value, index) {
  const safe = String(value == null ? '' : value).replace(/[^A-Za-z0-9_-]/g, '_');
  return baseId + '-opt-' + index + '-' + safe;
}

/* Text for the polite live region after the list changes. */
export function resultsMessage(count, query, noun) {
  const n = noun || 'channel';
  const plural = count === 1 ? n : n + 's';
  if (count === 0) return normalizeQuery(query) ? 'No ' + n + 's match.' : 'No ' + n + 's to choose from.';
  return count + ' ' + plural + (normalizeQuery(query) ? ' match.' : ' available.') + ' Use the up and down arrow keys to move through the list.';
}

/* What Escape does. With the list open it only closes the list and keeps
   the typed search, so a second Escape has something to clear. With the
   list closed and a search typed, it drops the search and the field shows
   the chosen option again. Otherwise Escape is left alone, so the dialog
   around the form can close. Returns 'close', 'clear' or null. */
export function escapeAction(open, query) {
  if (open) return 'close';
  if (query !== null && query !== undefined) return 'clear';
  return null;
}

/* Status line read out after the user presses Reload beside a picker, so a
   screen reader user hears that the list changed. */
export function reloadedMessage(count, noun) {
  const n = noun || 'channel';
  if (!count) return 'Channel list reloaded. No ' + n + 's found.';
  return 'Channel list reloaded. ' + count + ' ' + (count === 1 ? n : n + 's') + '.';
}
