import { signal } from '@preact/signals';

// A request from a page to open the Help drawer with a follow-up ticket
// started: { relatedTicketId, relatedSubject, body, returnFocus }. The drawer
// consumes it and sets it back to null. Kept here so pages do not import the
// drawer.
export const supportFollowUp = signal(null);

export function openSupportFollowUp({ relatedTicketId, relatedSubject = '', body = '', returnFocus = null }) {
  if (!relatedTicketId) return;
  supportFollowUp.value = { relatedTicketId, relatedSubject, body, returnFocus };
}

// A request from a page (the Support page "New ticket" button) to open the
// Help drawer: { returnFocus }. returnFocus is the element that gets focus
// back when the drawer closes; without it focus goes to the Help button.
export const supportDrawerRequest = signal(null);

export function openSupportDrawer({ returnFocus = null } = {}) {
  supportDrawerRequest.value = { returnFocus };
}

// How often an open ticket page checks for new replies, and the slower pace
// used once a ticket is closed.
export const TICKET_POLL_MS = 30000;
export const TICKET_POLL_CLOSED_MS = 120000;

// Matches SupportReopenWindow on the server. Only used for copy; the server
// sends reopen_until and decides.
export const REOPEN_DAYS = 7;

// The reopen deadline of a closed ticket as a Date, or null when there is
// none (open tickets, or closed tickets without a closed_at).
export function reopenDeadline(ticket) {
  if (ticket?.status !== 'closed' || !ticket.reopen_until) return null;
  const d = new Date(ticket.reopen_until);
  return Number.isNaN(d.getTime()) ? null : d;
}

// True when a closed ticket can no longer be reopened by reply.
export function reopenWindowPassed(ticket, now = Date.now()) {
  if (ticket?.status !== 'closed') return false;
  const d = reopenDeadline(ticket);
  return !d || d.getTime() < now;
}

// "Tuesday, December 1" style date for reopen deadlines.
export function deadlineText(d) {
  return d.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' });
}
