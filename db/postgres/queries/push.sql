-- name: CreatePushEndpoint :one
INSERT INTO push_endpoints (user_id, bot_id, channel_identity_id, name, token_hash)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetPushRecipientTarget :one
SELECT r.default_reply_target
FROM bot_channel_routes r
JOIN channel_identities ci ON ci.team_id = r.team_id AND ci.channel_type = r.channel_type
WHERE r.team_id = public.memoh_current_team_id()
  AND r.bot_id = sqlc.arg(bot_id) AND r.channel_config_id = sqlc.arg(channel_config_id)
  AND ci.id = sqlc.arg(channel_identity_id)
  AND r.conversation_type = 'private'
  AND r.metadata->>'sender_id' = ci.channel_subject_id
  AND COALESCE(r.default_reply_target, '') <> ''
ORDER BY r.updated_at DESC LIMIT 1;

-- name: ListPushEndpoints :many
SELECT * FROM push_endpoints
WHERE team_id = public.memoh_current_team_id() AND user_id = $1 ORDER BY created_at DESC;

-- name: GetPushEndpoint :one
SELECT * FROM push_endpoints WHERE team_id = public.memoh_current_team_id() AND id = $1;

-- name: UpdatePushEndpoint :one
UPDATE push_endpoints SET enabled = $3, updated_at = now()
WHERE team_id = public.memoh_current_team_id() AND id = $1 AND user_id = $2 RETURNING *;

-- name: RotatePushEndpointKey :one
UPDATE push_endpoints SET token_hash = $3, updated_at = now()
WHERE team_id = public.memoh_current_team_id() AND id = $1 AND user_id = $2 RETURNING *;

-- name: DeletePushEndpoint :execrows
DELETE FROM push_endpoints WHERE team_id = public.memoh_current_team_id() AND id = $1 AND user_id = $2;

-- name: ClaimPushDelivery :one
INSERT INTO push_deliveries (endpoint_id, dedupe_key, payload_hash) VALUES ($1, $2, $3)
ON CONFLICT (team_id, endpoint_id, dedupe_key) DO UPDATE
SET status = 'sending', attempts = push_deliveries.attempts + 1, error_code = '', updated_at = now()
WHERE push_deliveries.status = 'failed' AND push_deliveries.payload_hash = EXCLUDED.payload_hash
RETURNING *;

-- name: GetPushDelivery :one
SELECT * FROM push_deliveries
WHERE team_id = public.memoh_current_team_id() AND endpoint_id = $1 AND dedupe_key = $2;

-- name: FinishPushDelivery :exec
UPDATE push_deliveries SET status = $2, error_code = $3, updated_at = now()
WHERE team_id = public.memoh_current_team_id() AND id = $1 AND status = 'sending';

-- name: ListPushDeliveries :many
SELECT * FROM push_deliveries WHERE team_id = public.memoh_current_team_id() AND endpoint_id = $1
ORDER BY updated_at DESC LIMIT 20;

-- name: PrunePushDeliveries :exec
DELETE FROM push_deliveries WHERE team_id = public.memoh_current_team_id() AND endpoint_id = $1
AND updated_at < now() - interval '30 days';
