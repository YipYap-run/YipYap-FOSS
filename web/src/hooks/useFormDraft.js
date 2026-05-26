import { useState, useEffect } from 'preact/hooks';

export function useFormDraft(key, initialState) {
  const stored = sessionStorage.getItem(key);
  const [state, setState] = useState(() => {
    try { return stored ? JSON.parse(stored) : initialState; }
    catch { return initialState; }
  });
  const [hasDraft, setHasDraft] = useState(!!stored);

  useEffect(() => {
    const serialized = JSON.stringify(state);
    const initial = JSON.stringify(initialState);
    if (serialized !== initial) {
      sessionStorage.setItem(key, serialized);
      setHasDraft(true);
    } else {
      sessionStorage.removeItem(key);
      setHasDraft(false);
    }
  }, [state]);

  function discard() {
    sessionStorage.removeItem(key);
    setState(initialState);
    setHasDraft(false);
  }

  function clearDraft() {
    sessionStorage.removeItem(key);
    setHasDraft(false);
  }

  return [state, setState, { hasDraft, discard, clearDraft }];
}
