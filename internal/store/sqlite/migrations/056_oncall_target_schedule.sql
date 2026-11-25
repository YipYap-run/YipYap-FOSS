-- On-call escalation targets now point at a schedule (team implied) instead
-- of a team. Rewrite on-call targets whose target_id is a team id to that
-- team's first schedule (lowest id, deterministic). Rows already holding a
-- schedule id no longer match teams, so re-running is a no-op.
UPDATE step_targets
SET target_id = (
    SELECT MIN(s.id) FROM schedules s WHERE s.team_id = step_targets.target_id
)
WHERE target_type IN ('on_call_primary', 'on_call_secondary')
  AND target_id IN (SELECT id FROM teams)
  AND EXISTS (SELECT 1 FROM schedules s WHERE s.team_id = step_targets.target_id);

-- The old SPA saved on-call targets with an empty target_id, which paged
-- nobody. When the policy's org has exactly one schedule there is only one
-- reasonable reading, so point the target at it. Other rows stay empty and
-- the engine records a notify_failed event until the policy is edited.
UPDATE step_targets
SET target_id = (
    SELECT MIN(s.id)
    FROM schedules s
    JOIN teams t ON t.id = s.team_id
    JOIN escalation_steps es ON es.id = step_targets.step_id
    JOIN escalation_policies p ON p.id = es.policy_id
    WHERE t.org_id = p.org_id
)
WHERE target_type IN ('on_call_primary', 'on_call_secondary')
  AND target_id = ''
  AND (
    SELECT COUNT(*)
    FROM schedules s
    JOIN teams t ON t.id = s.team_id
    JOIN escalation_steps es ON es.id = step_targets.step_id
    JOIN escalation_policies p ON p.id = es.policy_id
    WHERE t.org_id = p.org_id
  ) = 1;
