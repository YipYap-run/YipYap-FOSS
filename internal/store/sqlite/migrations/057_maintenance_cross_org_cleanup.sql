-- Maintenance windows used to accept any monitor_id, so one org could attach
-- a window to another org's monitor and silence its alerts (mon-3). Remove
-- windows whose monitor belongs to a different org than the window. Windows
-- for deleted monitors and org-wide windows (empty monitor_id) are kept.
-- Re-running deletes nothing.
DELETE FROM maintenance_windows
WHERE monitor_id <> ''
  AND EXISTS (
    SELECT 1 FROM monitors m
    WHERE m.id = maintenance_windows.monitor_id
      AND m.org_id <> maintenance_windows.org_id
  );
