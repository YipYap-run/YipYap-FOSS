import './alerts.css';

export const SEV_LABEL = { critical: 'SEV1', warning: 'SEV2', info: 'SEV3' };

// SeverityTag is the SEV1/SEV2/SEV3 badge used on alert pages. withName
// adds the severity word ("SEV1 - critical") for the detail page header.
export function SeverityTag({ severity, withName = false, large = false }) {
  const known = Object.prototype.hasOwnProperty.call(SEV_LABEL, severity);
  const label = known ? SEV_LABEL[severity] : (severity || 'Unknown');
  const cls = `sev-badge${known ? ` sev-badge-${severity}` : ''}${large ? ' sev-badge-lg' : ''}`;
  return (
    <span class={cls}>
      {label}
      {withName && known ? ` - ${severity}` : ''}
    </span>
  );
}
