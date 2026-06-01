-- name: GetControlState :one
SELECT * FROM control_state;

-- name: SetControlMode :one
UPDATE control_state SET mode = $1, updated_at = NOW() RETURNING *;

-- name: EnqueueWorkUnit :one
INSERT INTO work_units (wp_ref, payload) VALUES ($1, $2) RETURNING *;

-- name: ClaimNextWorkUnit :one
UPDATE work_units SET status = 'in_flight', claimed_at = NOW()
WHERE id = (
    SELECT id FROM work_units
    WHERE status = 'queued'
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
) RETURNING *;

-- name: CompleteWorkUnit :one
UPDATE work_units SET status = 'done', completed_at = NOW()
WHERE id = $1 AND status = 'in_flight' RETURNING *;

-- name: FailWorkUnit :one
UPDATE work_units SET status = 'failed', error = $2, completed_at = NOW()
WHERE id = $1 AND status = 'in_flight' RETURNING *;

-- name: ListOrchestratorWorkUnits :many
SELECT * FROM work_units ORDER BY created_at;
