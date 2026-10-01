-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (
    user_id,
    token_hash,
    expires_at
)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(token_hash),
    sqlc.arg(expires_at)
)
RETURNING
    id,
    user_id,
    token_hash,
    expires_at,
    revoked_at,
    replaced_by_token_id,
    created_at;

-- name: GetRefreshTokenForUpdate :one
SELECT
    id,
    user_id,
    token_hash,
    expires_at,
    revoked_at,
    replaced_by_token_id,
    created_at
FROM refresh_tokens
WHERE token_hash = sqlc.arg(token_hash)
FOR UPDATE;

-- name: MarkRefreshTokenReplaced :one
UPDATE refresh_tokens
SET
    revoked_at = NOW(),
    replaced_by_token_id = sqlc.arg(replaced_by_token_id)
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL
  AND replaced_by_token_id IS NULL
RETURNING
    id,
    user_id,
    token_hash,
    expires_at,
    revoked_at,
    replaced_by_token_id,
    created_at;

-- name: RevokeRefreshTokenByHash :execrows
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL;

-- name: RevokeAllUserRefreshTokens :execrows
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE user_id = sqlc.arg(user_id)
  AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens
WHERE expires_at <= NOW();