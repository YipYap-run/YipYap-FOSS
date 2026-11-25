/* Pure helpers for the webhook signing secret UI. Kept free of Preact so
   `npm test` can check them against the API contract in
   internal/api/handlers/notification_channels_signing.go:

   - POST /notification-channels (type webhook) returns signing_secret once
     plus signing_secret_set: true.
   - GET and list return only signing_secret_set (webhook channels only).
   - POST /notification-channels/{id}/rotate-secret returns the channel plus
     signing_secret. 403 for members, 404 for another org, 400 for a
     channel that is not a webhook. */

export const TIMESTAMP_HEADER = 'X-YipYap-Timestamp';
export const SIGNATURE_HEADER = 'X-YipYap-Signature';
export const REPLAY_WINDOW_MINUTES = 5;

/* True for a webhook channel that the server reports as unsigned (made
   before signing existed). A missing flag is treated as unknown, not
   unsigned, so other channel types and older servers show nothing. */
export function isUnsignedWebhook(channel) {
  return !!channel && channel.type === 'webhook' && channel.signing_secret_set === false;
}

export function isSignedWebhook(channel) {
  return !!channel && channel.type === 'webhook' && channel.signing_secret_set === true;
}

/* Returns a copy of a channel response with the one-time secret removed, so
   it never lands in the cached channel list. */
export function withoutSecret(channel) {
  if (!channel || typeof channel !== 'object') return channel;
  const { signing_secret: _drop, ...rest } = channel;
  return rest;
}

/* Puts a rotate-secret response into the channel list without the secret.
   Avoids a full reload, which would unmount the modal that shows it. */
export function applyRotation(channels, res) {
  if (!Array.isArray(channels) || !res || !res.id) return channels;
  const clean = { ...withoutSecret(res), signing_secret_set: true };
  return channels.map(c => (c.id === res.id ? { ...c, ...clean } : c));
}

/* Copy for the inline confirmation before a rotate or first generate. */
export function rotatePrompt(signed) {
  if (signed) {
    return {
      title: 'Rotate the signing secret?',
      body: 'The current secret stops working right away. Requests are signed with the new secret from now on, so update your receiver as soon as you copy it.',
      confirm: 'Rotate secret',
      danger: true,
    };
  }
  return {
    title: 'Generate a signing secret?',
    body: 'Requests to this URL are signed from now on. Receivers that ignore the signature headers keep working.',
    confirm: 'Generate secret',
    danger: false,
  };
}

/* Maps a rotate-secret failure to a sentence for the user. */
export function interpretRotateError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can change the signing secret.';
  if (status === 404) return 'This channel no longer exists. Reload the page.';
  if (status === 400) return 'Only webhook channels have a signing secret.';
  const msg = err && err.message;
  return msg && !/^HTTP \d+$/.test(msg) ? msg : 'Could not change the signing secret. Try again.';
}

/* Closing the modal while a fresh secret is on screen and not yet copied
   asks once. The second close goes through. */
export function shouldWarnBeforeClose(shown, alreadyWarned) {
  return !!(shown && shown.secret && !shown.copied && !alreadyWarned);
}
