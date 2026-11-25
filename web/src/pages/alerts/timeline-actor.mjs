/* Words for ack and resolve rows on the alert timeline. Kept free of
   Preact so `npm test` can check them against the event contract:

   - GET /alerts/{id}/timeline returns alert events { event_type, channel,
     target_user_id, created_at }. For "ack" and "resolved", target_user_id
     is who acted: a YipYap user ID, or "Slack user <name>" when a Slack app
     button was pressed by someone with no matching YipYap account
     (slack_app_actions_full.go). channel names where it happened ("slack",
     "telegram", "discord"); empty for the web app or an automatic resolve.
   - GET /users returns the org's users { id, email, name }. */

const ACTIONS = {
  ack: 'Acknowledged',
  resolved: 'Resolved',
};

const CHANNELS = {
  slack: 'Slack',
  telegram: 'Telegram',
  discord: 'Discord',
};

const CHAT_ACTOR = /^(Slack|Telegram|Discord) user \S/;

/* Builds an id -> user map from a /users response (array or {users}). */
export function usersById(data) {
  const list = Array.isArray(data) ? data : (data && Array.isArray(data.users) ? data.users : []);
  const map = new Map();
  for (const u of list) if (u && u.id) map.set(u.id, u);
  return map;
}

function actorName(id, users) {
  if (!id) return '';
  const u = users && users.get(id);
  if (u) return (u.name || '').trim() || u.email || '';
  // A chat user with no YipYap account. Shown as text, so a Slack display
  // name cannot inject markup.
  if (CHAT_ACTOR.test(id)) return id;
  return '';
}

/* Returns { action, by, via } for an ack or resolve event, or null for any
   other event type. by and via are '' when unknown. A resolve with no actor
   and no channel came from the monitor recovering. */
export function ackResolveLine(evt, users) {
  if (!evt) return null;
  const type = String(evt.event_type || '');
  const action = ACTIONS[type];
  if (!action) return null;
  const channel = String(evt.channel || '').toLowerCase();
  const via = CHANNELS[channel] || '';
  const by = actorName(evt.target_user_id, users);
  const auto = type === 'resolved' && !evt.target_user_id && !channel;
  return { action, by, via, auto };
}

/* One sentence for screen readers and the timeline row, for example
   "Acknowledged in Slack by Sam Lee" or "Resolved automatically when the
   check passed". */
export function ackResolveText(line) {
  if (!line) return '';
  if (line.auto) return line.action + ' automatically when the monitor recovered';
  let s = line.action;
  if (line.via) s += ' in ' + line.via;
  if (line.by) s += ' by ' + line.by;
  return s;
}
