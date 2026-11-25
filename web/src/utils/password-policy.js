// Mirrors internal/auth/password_policy.go. The server enforces the policy;
// these values drive the inline hint and the checks before submit.
export const MIN_PASSWORD_LENGTH = 8;
// bcrypt input limit in bytes. Accented and non-Latin characters take two to
// four bytes each, so the limit in characters can be lower than this.
//
// Do not put this on an input as maxLength. The browser would cut a pasted
// password short without saying so, and a password manager would then save
// a password that does not match the account.
export const MAX_PASSWORD_BYTES = 72;

export const PASSWORD_HINT = `Use ${MIN_PASSWORD_LENGTH} to ${MAX_PASSWORD_BYTES} characters. A longer passphrase is stronger.`;

// passwordBytes returns the UTF-8 length the server measures.
export function passwordBytes(pw) {
  return new TextEncoder().encode(pw || '').length;
}

// passwordLength counts characters the way the server does (code points),
// so an emoji counts as one.
export function passwordLength(pw) {
  return [...(pw || '')].length;
}

// passwordTooLong reports a password the server will refuse for length.
export function passwordTooLong(pw) {
  return passwordBytes(pw) > MAX_PASSWORD_BYTES;
}

// passwordProblem returns the reason the server would refuse pw, or ''.
export function passwordProblem(pw) {
  if (passwordLength(pw) < MIN_PASSWORD_LENGTH) {
    return `Use at least ${MIN_PASSWORD_LENGTH} characters for the password.`;
  }
  if (passwordTooLong(pw)) {
    return tooLongMessage(pw);
  }
  return '';
}

function tooLongMessage(pw) {
  const extra = passwordBytes(pw) - MAX_PASSWORD_BYTES;
  const plain = passwordBytes(pw) === passwordLength(pw);
  return plain
    ? `This password is too long. Remove ${extra} character${extra === 1 ? '' : 's'} to get to ${MAX_PASSWORD_BYTES}.`
    : `This password is too long. The limit is ${MAX_PASSWORD_BYTES} bytes, and accented or non-Latin characters use 2 to 4 bytes each. Shorten it a little.`;
}

// describePasswordPolicyError turns the server's password_policy error into
// copy, or returns '' for any other error.
export function describePasswordPolicyError(err, pw) {
  if (err?.body?.code !== 'password_policy') return '';
  return passwordProblem(pw || '') ||
    `Use ${err.body.min_length || MIN_PASSWORD_LENGTH} to ${err.body.max_length || MAX_PASSWORD_BYTES} characters for the password.`;
}
