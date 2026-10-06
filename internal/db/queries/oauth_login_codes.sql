-- name: CreateOAuthLoginCode :one
INSERT INTO oauth_login_codes (
    user_id,
    code_hash,
    expires_at
)
VALUES ($1, $2, $3)
RETURNING id, user_id, code_hash, expires_at, consumed_at, created_at;

-- name: GetOAuthLoginCodeForUpdate :one
SELECT
    olc.id,
    olc.user_id,
    olc.expires_at,
    olc.consumed_at,
    u.role
FROM oauth_login_codes AS olc
JOIN users AS u ON u.id = olc.user_id
WHERE olc.code_hash = $1
FOR UPDATE OF olc;

-- name: ConsumeOAuthLoginCode :one
UPDATE oauth_login_codes
SET consumed_at = $1
WHERE id = $2
  AND consumed_at IS NULL
RETURNING id;

-- name: DeleteExpiredOAuthLoginCodes :execrows
DELETE FROM oauth_login_codes
WHERE expires_at <= NOW();
