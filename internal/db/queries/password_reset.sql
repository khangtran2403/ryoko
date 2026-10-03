-- Locking the user first gives request, verification, and confirmation
-- transactions a consistent lock order.

-- name: LockUserForPasswordResetByEmail :one
SELECT
    id,
    email
FROM users
WHERE lower(email) = lower(sqlc.arg(email))
FOR UPDATE;

-- name: LockUserForPasswordResetByID :one
SELECT
    id,
    email
FROM users
WHERE id = sqlc.arg(user_id)
FOR UPDATE;

-- Used to enforce the request cooldown.
-- name: GetLatestPasswordResetRequest :one
SELECT
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at
FROM password_reset_requests
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- Invalidates any previous OTP or verified reset token.
-- name: ConsumeActivePasswordResetRequests :execrows
UPDATE password_reset_requests
SET consumed_at = sqlc.arg(consumed_at)
WHERE user_id = sqlc.arg(user_id)
  AND consumed_at IS NULL;

-- name: CreatePasswordResetRequest :one
INSERT INTO password_reset_requests (
    user_id,
    otp_hash,
    expires_at
)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(otp_hash),
    sqlc.arg(expires_at)
)
RETURNING
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at;

-- Call this only after locking the user row.
-- Returning expired or exhausted requests lets the service map every
-- invalid state to one public error.
-- name: GetActivePasswordResetRequestForUpdate :one
SELECT
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at
FROM password_reset_requests
WHERE user_id = sqlc.arg(user_id)
  AND consumed_at IS NULL
  AND verified_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: IncrementPasswordResetAttempts :one
UPDATE password_reset_requests
SET attempts = attempts + 1
WHERE id = sqlc.arg(id)
  AND attempts < 5
  AND verified_at IS NULL
  AND consumed_at IS NULL
RETURNING attempts;

-- Exchanges a correct OTP for a hashed opaque reset token.
-- name: MarkPasswordResetVerified :one
UPDATE password_reset_requests
SET
    verified_at = sqlc.arg(verified_at),
    reset_token_hash = sqlc.arg(reset_token_hash),
    reset_token_expires_at = sqlc.arg(reset_token_expires_at)
WHERE id = sqlc.arg(id)
  AND attempts < 5
  AND verified_at IS NULL
  AND consumed_at IS NULL
  AND expires_at > sqlc.arg(verified_at)
RETURNING
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at;

-- First lookup for confirmation. The service should then lock the user
-- and reload this request with GetPasswordResetRequestForUpdate.
-- name: GetPasswordResetRequestByTokenHash :one
SELECT
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at
FROM password_reset_requests
WHERE reset_token_hash = sqlc.arg(reset_token_hash);

-- name: GetPasswordResetRequestForUpdate :one
SELECT
    id,
    user_id,
    otp_hash,
    attempts,
    expires_at,
    verified_at,
    reset_token_hash,
    reset_token_expires_at,
    consumed_at,
    created_at
FROM password_reset_requests
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateUserPasswordAfterReset :one
UPDATE users
SET
    password_hash = sqlc.arg(password_hash)::text,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(user_id)
RETURNING id;

-- Call after updating the password and revoking refresh sessions,
-- within the same transaction.
-- name: ConsumePasswordResetRequest :one
UPDATE password_reset_requests
SET consumed_at = sqlc.arg(consumed_at)
WHERE id = sqlc.arg(id)
  AND consumed_at IS NULL
  AND verified_at IS NOT NULL
  AND reset_token_expires_at > sqlc.arg(consumed_at)
RETURNING user_id;

-- Optional cleanup query for a future worker.
-- name: DeleteExpiredPasswordResetRequests :execrows
DELETE FROM password_reset_requests
WHERE consumed_at IS NOT NULL
   OR (
        reset_token_expires_at IS NULL
        AND expires_at <= NOW()
   )
   OR reset_token_expires_at <= NOW();