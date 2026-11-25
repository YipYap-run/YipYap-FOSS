import { signal, effect } from '@preact/signals';
import { initialTheme, isTheme, writeStoredTheme } from './theme-store.mjs';

export const theme = signal(initialTheme(window));

// Sync to the DOM and to localStorage whenever the theme changes. Saving is
// best effort: with site data blocked the theme still applies for this page.
effect(() => {
  const value = isTheme(theme.value) ? theme.value : 'light';
  document.documentElement.setAttribute('data-theme', value);
  writeStoredTheme(window, value);
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
