-- Step targets now honor "simultaneous": a target that is not simultaneous
-- pages after the one before it instead of together with it. Until now the
-- engine paged every target of a step at once and the flag defaulted to
-- false, so set it on existing targets to keep how they page today. Runs
-- once (by version); new saves set the flag explicitly.
UPDATE step_targets SET simultaneous = 1 WHERE simultaneous = 0;
