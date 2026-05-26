export function bufferToBase64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let str = '';
  for (const b of bytes) str += String.fromCharCode(b);
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function base64urlToBuffer(base64url) {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
  const pad = base64.length % 4;
  const padded = pad ? base64 + '='.repeat(4 - pad) : base64;
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

export function prepareCreationOptions(options) {
  return {
    ...options,
    challenge: base64urlToBuffer(options.challenge),
    user: { ...options.user, id: base64urlToBuffer(options.user.id) },
    excludeCredentials: (options.excludeCredentials || []).map(c => ({
      ...c, id: base64urlToBuffer(c.id),
    })),
  };
}

export function serializeCredential(cred) {
  return {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bufferToBase64url(cred.response.attestationObject),
      clientDataJSON: bufferToBase64url(cred.response.clientDataJSON),
    },
  };
}

export function prepareAssertionOptions(options) {
  return {
    ...options,
    challenge: base64urlToBuffer(options.challenge),
    allowCredentials: (options.allowCredentials || []).map(c => ({
      ...c, id: base64urlToBuffer(c.id),
    })),
  };
}

export function serializeAssertion(assertion) {
  return {
    id: assertion.id,
    rawId: bufferToBase64url(assertion.rawId),
    type: assertion.type,
    response: {
      authenticatorData: bufferToBase64url(assertion.response.authenticatorData),
      clientDataJSON: bufferToBase64url(assertion.response.clientDataJSON),
      signature: bufferToBase64url(assertion.response.signature),
      userHandle: assertion.response.userHandle
        ? bufferToBase64url(assertion.response.userHandle)
        : null,
    },
  };
}
