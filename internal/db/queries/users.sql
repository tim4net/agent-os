-- name: GetUserByLogin :one
SELECT id, login, display_name, is_active, created_at, updated_at
FROM users WHERE login = $1;

-- name: GetUserByID :one
SELECT id, login, display_name, is_active, created_at, updated_at
FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (login, display_name)
VALUES ($1, $2)
RETURNING id, login, display_name, is_active, created_at, updated_at;

-- name: ListUsers :many
SELECT id, login, display_name, is_active, created_at, updated_at
FROM users ORDER BY created_at;
