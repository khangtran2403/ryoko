-- name: CreateUser :one
INSERT INTO users (email, full_name, phone)
VALUES ($1, $2, $3)
RETURNING id, email, full_name, phone, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, full_name, phone, created_at, updated_at
FROM users
WHERE id = $1;

-- name: UpdateUser :one
UPDATE users
SET
    email = $2,
    full_name = $3,
    phone = $4,
    updated_at = now()
WHERE id = $1
RETURNING id, email, full_name, phone, created_at, updated_at;

-- name: DeleteUser :one
DELETE FROM users
WHERE id = $1
RETURNING id;