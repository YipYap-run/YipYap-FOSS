// Names for the people on a support ticket. The API returns "Former member"
// as opened_by when the opener has left the org; mid-sentence it reads
// "a former member".

export const FORMER_MEMBER = 'Former member';

// openerName returns who opened the ticket. With sentence set, the result is
// meant for the middle of a sentence ("Opened by you", "a former member").
export function openerName(ticket, { sentence = false } = {}) {
  if (ticket?.is_mine) return sentence ? 'you' : 'You';
  const name = (ticket?.opened_by || '').trim();
  if (!name || name === FORMER_MEMBER) return sentence ? 'a former member' : FORMER_MEMBER;
  return name;
}
