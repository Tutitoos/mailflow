-- name: GetUserByAuthSubject :one
SELECT id, email, locale
FROM users
WHERE id = $1;
