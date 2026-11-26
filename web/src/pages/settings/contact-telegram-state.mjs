// Pure helpers for connecting a personal Telegram contact method. Kept free
// of Preact so `node --test` can check them against the API in
// internal/api/handlers/telegram_link_contact.go:
//
//   POST /telegram/link-codes {purpose: "contact_method"} (any member)
//     201 {id, status: "pending", expires_at, code, command, bot_username,
//          private_link}
//     403 plan gate {upgrade_required} or API key, 409 contact method
//     limit, 429 too many codes this hour, 503 linking not configured.
//   GET /telegram/link-codes/{id} (only the user who made the code)
//     200 {id, status, expires_at, contact_method_id, chat_title (linked)}
//
// The user sends the code to the bot from their own private chat. The bot
// then adds (or verifies) their Telegram contact method on the YipYap bot.
// GET users/me/contact-method-options says whether that works here
// (telegram_linking) and which org Telegram channels use their own bot
// (integrations[].own_bot).

import { statusMessage } from './telegram-link-state.mjs';

export const CONTACT_LINK_REQUEST = Object.freeze({ purpose: 'contact_method' });

// The org's Telegram channels that send through their own bot. YipYap
// cannot read messages sent to those bots, so a method on one still needs
// a typed chat ID.
export function ownBotIntegrations(options) {
  const list = (options && Array.isArray(options.integrations)) ? options.integrations : [];
  return list.filter(i => i && i.type === 'telegram' && i.own_bot);
}

// How the add form offers Telegram:
//   'connect'     the YipYap bot can link the account with a code
//   'manual'      no YipYap bot, but the org has its own bot: type the ID
//   'unavailable' neither
export function telegramContactMode(options) {
  if (options && options.telegram_linking) return 'connect';
  if (ownBotIntegrations(options).length > 0) return 'manual';
  return 'unavailable';
}

// A method that sends through the YipYap bot (no via_channel_id). Its chat
// is set by Connect Telegram, so the edit form does not offer the field.
export function isPlatformTelegramMethod(method) {
  if (!method || method.type !== 'telegram') return false;
  const c = method.config || {};
  return !c.via_channel_id;
}

export function contactStatusMessage(status, chatTitle, soon = false) {
  switch (status) {
    case 'linked':
      return chatTitle ? 'Connected to ' + chatTitle + '.' : 'Connected.';
    case 'expired':
      return 'This code expired. Press Connect Telegram to get a new one.';
    case 'failed':
      return 'Telegram could not be connected. Check the bot reply in Telegram, then try again.';
    default:
      return statusMessage(status, '', soon);
  }
}

export function interpretContactLinkError(err) {
  const status = err && err.status;
  const raw = (err && err.body && err.body.error) || (err && err.message) || '';
  switch (status) {
    case 503:
      return { message: 'Telegram is not set up on this server. Ask your YipYap operator to configure the Telegram bot.', unavailable: true };
    case 429:
      return { message: 'Too many link codes this hour. Try again later.', unavailable: false };
    case 409:
    case 403:
      return { message: raw ? sentence(raw) : 'You cannot add a Telegram contact method right now.', unavailable: true };
    default:
      return { message: raw ? sentence(raw) : 'Could not start connecting Telegram. Try again.', unavailable: false };
  }
}

function sentence(msg) {
  const s = String(msg).trim();
  if (!s) return s;
  const out = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(out) ? out : out + '.';
}
