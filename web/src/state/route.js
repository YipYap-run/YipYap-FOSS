import { signal } from '@preact/signals';

// Current router path, updated by the router's onChange handler in app.jsx.
// Reading this in a component makes the component re-render on client-side
// navigation (window.location.pathname does not trigger a re-render on its own).
export const currentPath = signal(
  typeof window !== 'undefined' ? window.location.pathname : '/'
);
