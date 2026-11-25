// Theme storage helpers with no DOM side effects, so they can be unit tested.
// Browsers that block site data throw on any localStorage access (including
// reading the property), so every access is wrapped. public/theme-init.js
// repeats this logic in plain ES5 because it runs before the bundle loads.

export const STORAGE_KEY = 'yipyap-theme';
export const THEMES = ['light', 'dark', 'oled'];

export function isTheme(value) {
  return THEMES.indexOf(value) !== -1;
}

export function prefersDark(win) {
  try {
    return !!(win && typeof win.matchMedia === 'function' &&
      win.matchMedia('(prefers-color-scheme: dark)').matches);
  } catch {
    return false;
  }
}

export function readStoredTheme(win) {
  try {
    const value = win.localStorage.getItem(STORAGE_KEY);
    return isTheme(value) ? value : null;
  } catch {
    return null;
  }
}

// Returns true when the value was saved.
export function writeStoredTheme(win, value) {
  if (!isTheme(value)) return false;
  try {
    win.localStorage.setItem(STORAGE_KEY, value);
    return true;
  } catch {
    return false;
  }
}

export function initialTheme(win) {
  return readStoredTheme(win) || (prefersDark(win) ? 'dark' : 'light');
}
