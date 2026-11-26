// FOSS build: no event delivery channels, so there is nothing to add to the
// channel form, plan notices or alert timeline.
export const EVENTING_CHANNEL_TYPES = {};
export const EVENTING_GUARDED_URL_FIELDS = {};
export const EVENTING_PAID_TYPES = [];
export const EVENTING_PAID_KINDS = {};
export const EVENTING_CHANNEL_LABELS = {};
export const EVENTING_JSON_FIELDS = {};
export const REPLY_TIMELINE_LABELS = {};
export const REPLY_TIMELINE_TONES = {};
export const EVENTING_NAV_ITEMS = [];

export function eventingServerErrorField() {
  return null;
}

export function eventingSubfieldLabel() {
  return '';
}

export function testFilteredMessage() {
  return null;
}
