import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post } from '../../api/client';
import { route } from 'preact-router';
import { currentUser, completeLogin, logout } from '../../state/auth';
import { inviteTokenFromSearch, checkAcceptForm, roleWithArticle, acceptErrorView, acceptResult, PASSWORD_MIN, PASSWORD_RULE, NAME_MAX } from '../settings/invites-view.mjs';

// AcceptInvitePage is the public page an invite link opens (org-6, decision
// core-billing-org#1). It shows who is joining which org, then creates the
// account with the password the invitee picks.
export function AcceptInvitePage() {
  const [token] = useState(() => inviteTokenFromSearch(typeof window !== 'undefined' ? window.location.search : ''));
  const [invite, setInvite] = useState(null);
  const [loading, setLoading] = useState(!!token);
  const [fatal, setFatal] = useState(null);
  const [loadError, setLoadError] = useState('');
  const [form, setForm] = useState({ name: '', password: '', confirm: '' });
  const [fieldError, setFieldError] = useState(null);
  const [formError, setFormError] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(null);
  const headingRef = useRef(null);

  async function load() {
    setLoading(true);
    setLoadError('');
    try {
      const res = await get(`/invites/accept?token=${encodeURIComponent(token)}`);
      if (res?.account_exists) setFatal(acceptErrorView({ status: 409, body: { code: 'account_exists' } }));
      else setInvite(res);
    } catch (err) {
      const v = acceptErrorView(err);
      if (v.kind === 'fatal') setFatal(v);
      else setLoadError(v.message);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (token) load();
    else setFatal(acceptErrorView({ status: 404 }));
  }, []);

  // Each state change replaces the card; move focus to its heading so
  // screen reader users hear the new state.
  useEffect(() => { if (!loading) headingRef.current?.focus(); }, [loading, !!fatal, !!done]);

  async function submit(e) {
    e.preventDefault();
    if (submitting) return;
    setFormError(null);
    const problem = checkAcceptForm(form);
    setFieldError(problem);
    if (problem) {
      document.getElementById(`accept-${problem.field}`)?.focus();
      return;
    }
    setSubmitting(true);
    try {
      const res = await post('/invites/accept', { token, name: form.name.trim(), password: form.password });
      setForm({ name: '', password: '', confirm: '' });
      const out = acceptResult(res);
      if (out.signedIn) {
        // The server started a session for the new account; go to the app.
        try {
          await completeLogin(out.token, out.user);
          route('/', true);
          return;
        } catch (_) {
          // The account exists; fall back to the sign-in prompt below.
        }
      }
      setDone({ email: out.email || invite?.email });
    } catch (err) {
      const v = acceptErrorView(err);
      if (v.kind === 'fatal') setFatal(v);
      else setFormError(v);
    } finally {
      setSubmitting(false);
    }
  }

  async function signOutAndSignIn() {
    await logout();
    route('/login');
  }

  const signedInAs = currentUser.value?.email;

  let title = 'Join your team';
  let body;
  if (loading) {
    title = 'Checking your invite';
    body = <div class="loading-screen" role="status"><div class="spinner" aria-hidden="true" /><p>Checking your invite...</p></div>;
  } else if (done) {
    title = 'You have joined';
    body = (
      <div>
        <p class="auth-message">Your account for <strong>{done.email}</strong> is ready. Sign in with your email and the password you just set.</p>
        {signedInAs ? (
          <>
            <p class="auth-message">You are still signed in as {signedInAs}. Sign out to use the new account.</p>
            <button type="button" class="btn btn-primary btn-full" onClick={signOutAndSignIn}>Sign out and sign in</button>
          </>
        ) : (
          <a href="/login" class="btn btn-primary btn-full">Sign in</a>
        )}
      </div>
    );
  } else if (fatal) {
    title = fatal.title;
    body = (
      <div>
        <p class="auth-message">{fatal.message}</p>
        {fatal.action === 'login' && <a href="/login" class="btn btn-primary btn-full">Sign in</a>}
      </div>
    );
  } else if (loadError) {
    title = 'Could not check your invite';
    body = (
      <div>
        <p class="form-error" role="alert">{loadError}</p>
        <button type="button" class="btn btn-primary btn-full" onClick={load}>Try again</button>
      </div>
    );
  } else if (invite) {
    title = `Join ${invite.org_name}`;
    const err = (f) => fieldError?.field === f ? fieldError.message : '';
    body = (
      <form onSubmit={submit} noValidate>
        <p class="auth-message">
          You were invited to join <strong>{invite.org_name}</strong> on YipYap as {roleWithArticle(invite.role)}.
          {invite.expires_at && <> The invite expires {new Date(invite.expires_at).toLocaleString()}.</>}
        </p>
        {signedInAs && signedInAs !== invite.email && (
          <p class="auth-message">You are signed in as {signedInAs}. This invite creates a separate account for {invite.email}, and joining signs you in to it.</p>
        )}
        {formError && (
          <div class="form-error" role="alert">
            <strong>{formError.title}.</strong> {formError.message}
          </div>
        )}
        <div class="form-group">
          <label for="accept-email">Email</label>
          <input id="accept-email" type="email" value={invite.email} readOnly autocomplete="username" aria-describedby="accept-email-help" />
          <p id="accept-email-help" class="form-help">You sign in with this email.</p>
        </div>
        <div class="form-group">
          <label for="accept-name">Name <span class="text-muted">(optional)</span></label>
          <input id="accept-name" type="text" autocomplete="name" maxLength={NAME_MAX} value={form.name}
                 aria-invalid={err('name') ? 'true' : undefined} aria-describedby={err('name') ? 'accept-name-error' : undefined}
                 onInput={e => setForm({ ...form, name: e.target.value })} />
          {err('name') && <p id="accept-name-error" class="form-error">{err('name')}</p>}
        </div>
        <div class="form-group">
          <label for="accept-password">Password</label>
          <input id="accept-password" type="password" autocomplete="new-password" required minLength={PASSWORD_MIN} value={form.password}
                 aria-invalid={err('password') ? 'true' : undefined}
                 aria-describedby={err('password') ? 'accept-password-help accept-password-error' : 'accept-password-help'}
                 onInput={e => setForm({ ...form, password: e.target.value })} />
          <p id="accept-password-help" class="form-help">{PASSWORD_RULE}</p>
          {err('password') && <p id="accept-password-error" class="form-error">{err('password')}</p>}
        </div>
        <div class="form-group">
          <label for="accept-confirm">Confirm password</label>
          <input id="accept-confirm" type="password" autocomplete="new-password" required value={form.confirm}
                 aria-invalid={err('confirm') ? 'true' : undefined} aria-describedby={err('confirm') ? 'accept-confirm-error' : undefined}
                 onInput={e => setForm({ ...form, confirm: e.target.value })} />
          {err('confirm') && <p id="accept-confirm-error" class="form-error">{err('confirm')}</p>}
        </div>
        <button type="submit" class="btn btn-primary btn-full" disabled={submitting} aria-busy={submitting ? 'true' : undefined}>
          {submitting ? 'Creating account...' : 'Create account and join'}
        </button>
      </form>
    );
  }

  return (
    <div class="auth-page">
      <main class="auth-card accept-invite">
        <div class="auth-header">
          <div class="logo" aria-hidden="true">YipYap</div>
          <h1 ref={headingRef} tabIndex={-1} class="accept-invite-title">{title}</h1>
        </div>
        {body}
      </main>
    </div>
  );
}
