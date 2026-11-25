// Delivery health on the notification channel list. The text comes from
// the API's sanitized health fields and is rendered as plain text.
import { useState, useEffect, useRef } from 'preact/hooks';
import { channelHealthView, failingChannelsNote, linkedChannel } from './channel-health-logic.mjs';
import './channel-health.css';

export function ChannelHealthBadge({ channel }) {
  const v = channelHealthView(channel?.health);
  if (!v || !v.badge) return null;
  // The badge is a text word, not a color. The hidden prefix gives screen
  // readers the context ("Delivery status: Failing") the layout gives
  // sighted users.
  return (
    <span class={`channel-health-badge channel-health-${v.tone}`}>
      <span class="channel-health-sr">Delivery status: </span>{v.badge}
    </span>
  );
}

export function ChannelHealthLine({ channel }) {
  const v = channelHealthView(channel?.health);
  if (!v || !v.detail) return null;
  return (
    <span class={`channel-health-line channel-health-line-${v.status}`}>
      {v.detail}
    </span>
  );
}

export function FailingChannelsNote({ channels, canManage }) {
  const text = failingChannelsNote(channels, { canManage });
  if (!text) return null;
  return <p class="channel-failing-note" role="note">{text}</p>;
}

// channelRowId is the DOM id of a channel's row on the list.
export function channelRowId(id) {
  return 'channel-row-' + id;
}

// useLinkedChannel handles the link in the owner failure email
// (/settings/channels?channel=ID). Once the list has loaded it scrolls to
// that channel's row, moves focus there so keyboard and screen reader
// users start on it, and returns its id so the row can be highlighted.
// When the channel is gone it says so instead of leaving the person to
// search. It runs once per page load.
export function useLinkedChannel(channels, loading, toast) {
  const [target, setTarget] = useState(null);
  const done = useRef(false);
  useEffect(() => {
    if (loading || done.current || typeof window === 'undefined') return;
    done.current = true;
    const link = linkedChannel(window.location.search, channels);
    if (!link) return;
    if (!link.found) {
      // An empty list is either a failed load (already reported) or an
      // org with no channels, where the empty state says enough.
      if (toast && Array.isArray(channels) && channels.length > 0) {
        toast('The channel from that link no longer exists. It may have been deleted.', 'error');
      }
      return;
    }
    setTarget(link.id);
    setTimeout(() => {
      const el = document.getElementById(channelRowId(link.id));
      if (!el) return;
      const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
      el.scrollIntoView({ block: 'center', behavior: reduce ? 'auto' : 'smooth' });
      el.focus({ preventScroll: true });
    }, 0);
  }, [loading, channels]);
  return target;
}
