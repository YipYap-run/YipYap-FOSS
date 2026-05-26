import { signal, effect } from '@preact/signals';

const STORAGE_KEY = 'yipyap-theme';

function getInitialTheme() {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'light' || stored === 'dark' || stored === 'oled') return stored;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export const theme = signal(getInitialTheme());

// Sync to DOM and localStorage whenever theme changes
effect(() => {
  document.documentElement.setAttribute('data-theme', theme.value);
  localStorage.setItem(STORAGE_KEY, theme.value);
});

export function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light';
}

export function setOLED(enabled) {
  theme.value = enabled ? 'oled' : 'dark';
}

export function isDark() {
  return theme.value === 'dark' || theme.value === 'oled';
}
