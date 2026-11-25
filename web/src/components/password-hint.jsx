import { PASSWORD_HINT, passwordTooLong, passwordProblem } from '../utils/password-policy';
import '../styles/account-email.css';

// PasswordHint is the requirement text under a new-password field. Point the
// input's aria-describedby at `id`. When the password goes over the byte
// limit it says so right away in a polite live region, since the field has
// no maxLength (that would cut a pasted password short without telling).
export function PasswordHint({ id, value, class: cls = 'form-help' }) {
  const tooLong = passwordTooLong(value);
  return (
    <div id={id} aria-live="polite">
      <p class={cls} style="margin-bottom:0">{PASSWORD_HINT}</p>
      {tooLong && <p class="password-hint-problem">{passwordProblem(value)}</p>}
    </div>
  );
}
