// Status and priority pills for support tickets. White text on these shades
// is at least 4.5:1 (the shared SUPPORT_*_COLORS in ui.jsx are 2.2 to 3.8:1).
// Each pill carries a hidden "Status:" or "Priority:" prefix so a screen
// reader does not read two bare words in a row ("open normal").

const STATUS = {
  open: { bg: '#15803d', label: 'Open' },
  pending: { bg: '#b45309', label: 'Pending' },
  closed: { bg: '#4b5563', label: 'Closed' },
};

const PRIORITY = {
  low: { bg: '#4b5563', label: 'Low' },
  normal: { bg: '#1d4ed8', label: 'Normal' },
  high: { bg: '#c2410c', label: 'High' },
  urgent: { bg: '#b91c1c', label: 'Urgent' },
};

function Pill({ kind, map, value }) {
  const hit = map[value];
  const label = hit ? hit.label : (value || 'Unknown');
  return (
    <span class="support-pill" style={{ background: hit ? hit.bg : '#4b5563' }}>
      <span class="sr-only">{kind}: </span>{label}
    </span>
  );
}

export function StatusPill({ value }) {
  return <Pill kind="Status" map={STATUS} value={value} />;
}

export function PriorityPill({ value }) {
  return <Pill kind="Priority" map={PRIORITY} value={value} />;
}
