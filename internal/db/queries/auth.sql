-- name: RegisterUser :one
INSERT INTO users (
    email,
    full_name,
    phone,
    password_hash
)
VALUES ($1, $2, $3, $4)
RETURNING
    id,
    email,
    full_name,
    phone,
    role,
    created_at,
    updated_at;

-- name: GetUserForLogin :one
SELECT
    id,
    email,
    full_name,
    phone,
    password_hash,
    role,
    created_at,
    updated_at
FROM users
WHERE lower(email) = lower($1);