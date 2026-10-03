-- Used for normal OAuth login after the identity has already been linked.
-- name: GetOAuthUserByProviderSubject :one
SELECT
    oa.id AS oauth_account_id,
    oa.user_id,
    oa.provider,
    oa.provider_user_id,
    oa.provider_email,
    u.email,
    u.full_name,
    u.role
FROM oauth_accounts AS oa
JOIN users AS u
    ON u.id = oa.user_id
WHERE oa.provider = sqlc.arg(provider)
  AND oa.provider_user_id = sqlc.arg(provider_user_id);

-- Rechecked inside the account-linking transaction.
-- Lock the OAuth account before changing its stored provider email.
-- name: GetOAuthAccountForUpdate :one
SELECT
    id,
    user_id,
    provider,
    provider_user_id,
    provider_email,
    created_at,
    updated_at
FROM oauth_accounts
WHERE provider = sqlc.arg(provider)
  AND provider_user_id = sqlc.arg(provider_user_id)
FOR UPDATE;

-- Creates a passwordless customer when the email is new.
-- If the verified Google email already belongs to a Ryoko user, this performs
-- a no-op update and returns that existing user while locking its row.
--
-- The conflict target matches users_email_unique_ci.
-- name: GetOrCreateOAuthUser :one
INSERT INTO users (
    email,
    full_name
)
VALUES (
    sqlc.arg(email),
    sqlc.arg(full_name)
)
ON CONFLICT (lower(email))
DO UPDATE SET
    email = users.email
RETURNING
    id,
    email,
    full_name,
    role,
    created_at,
    updated_at;

-- name: CreateOAuthAccount :one
INSERT INTO oauth_accounts (
    user_id,
    provider,
    provider_user_id,
    provider_email
)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(provider),
    sqlc.arg(provider_user_id),
    sqlc.arg(provider_email)
)
RETURNING
    id,
    user_id,
    provider,
    provider_user_id,
    provider_email,
    created_at,
    updated_at;

-- Keep the provider-email snapshot current when Google reports a changed
-- verified email. This does not automatically change users.email.
-- name: UpdateOAuthAccountEmail :one
UPDATE oauth_accounts
SET
    provider_email = sqlc.arg(provider_email),
    updated_at = NOW()
WHERE id = sqlc.arg(oauth_account_id)
RETURNING
    id,
    user_id,
    provider,
    provider_user_id,
    provider_email,
    created_at,
    updated_at;