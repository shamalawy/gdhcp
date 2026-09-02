// postgres_user.go implements UserStore: users and API tokens (amendments
// A14 token hashing, A15 RBAC role column).
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

const userSelectCols = `id, username, email, password_hash, role, description, is_active, created_at, updated_at, created_by_user_id, updated_by_user_id`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	var createdBy, updatedBy *int64
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.Description, &u.IsActive,
		&u.CreatedAt, &u.UpdatedAt, &createdBy, &updatedBy); err != nil {
		return nil, err
	}
	if createdBy != nil {
		u.CreatedByUser = &models.User{ID: *createdBy}
	}
	if updatedBy != nil {
		u.UpdatedByUser = &models.User{ID: *updatedBy}
	}
	return &u, nil
}

func (p *Postgres) GetUserByID(ctx context.Context, userID int64) (*models.User, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+userSelectCols+` FROM users WHERE id = $1`, userID)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapReadError(err, fmt.Sprintf("get user %d", userID))
	}
	return u, nil
}

func (p *Postgres) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+userSelectCols+` FROM users WHERE username = $1`, username)
	u, err := scanUser(row)
	if err != nil {
		return nil, mapReadError(err, "get user "+username)
	}
	return u, nil
}

func (p *Postgres) CreateUser(ctx context.Context, user *models.User) error {
	if !user.Role.Valid() {
		return fmt.Errorf("store: create user: invalid role %q: %w", user.Role, models.ErrInvalidInput)
	}
	var createdBy any
	if user.CreatedByUser != nil {
		createdBy = user.CreatedByUser.ID
	}
	err := p.pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, description, is_active, created_by_user_id, updated_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING id, created_at, updated_at`,
		user.Username, user.Email, user.PasswordHash, user.Role, user.Description, user.IsActive, createdBy,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return mapWriteError(err, "create user")
	}
	return nil
}

func (p *Postgres) UpdateUser(ctx context.Context, user *models.User) error {
	if !user.Role.Valid() {
		return fmt.Errorf("store: update user %d: invalid role %q: %w", user.ID, user.Role, models.ErrInvalidInput)
	}
	var updatedBy any
	if user.UpdatedByUser != nil {
		updatedBy = user.UpdatedByUser.ID
	}
	err := p.pool.QueryRow(ctx, `
		UPDATE users SET username=$2, email=$3, role=$4, description=$5, is_active=$6, updated_by_user_id=$7, updated_at=now()
		WHERE id = $1 RETURNING updated_at`,
		user.ID, user.Username, user.Email, user.Role, user.Description, user.IsActive, updatedBy,
	).Scan(&user.UpdatedAt)
	if err != nil {
		return mapWriteError(mapReadError(err, fmt.Sprintf("update user %d", user.ID)), "update user")
	}
	return nil
}

func (p *Postgres) DeleteUser(ctx context.Context, userID int64) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return mapWriteError(err, "delete user")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete user %d: %w", userID, models.ErrNotFound)
	}
	return nil
}

func (p *Postgres) ListUsers(ctx context.Context, params ListParams) (ListResult[*models.User], error) {
	limit, offset := normalizeListParams(params)
	search := nilIfEmpty(params.Search)
	role := nilIfEmpty(params.Filters["role"])

	const where = `WHERE ($1::text IS NULL OR username ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%')
		AND ($2::text IS NULL OR role = $2)`

	var total int64
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM users `+where, search, role).Scan(&total); err != nil {
		return ListResult[*models.User]{}, fmt.Errorf("store: count users: %w", err)
	}

	order := orderByClause(params.Sort, map[string]string{"username": "username", "created_at": "created_at"}, "username ASC")
	rows, err := p.pool.Query(ctx, `SELECT `+userSelectCols+` FROM users `+where+
		` ORDER BY `+order+` LIMIT $3 OFFSET $4`, search, role, limit, offset)
	if err != nil {
		return ListResult[*models.User]{}, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	var items []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return ListResult[*models.User]{}, fmt.Errorf("store: list users: %w", err)
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return ListResult[*models.User]{}, fmt.Errorf("store: list users: %w", err)
	}
	return ListResult[*models.User]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// ---------------------------------------------------------------------------
// API tokens (amendment A14: token_hash is SHA-256 hex of the raw token)
// ---------------------------------------------------------------------------

const tokenSelectCols = `id, user_id, token_hash, description, last_used_at, expires_at, created_at`

func scanToken(row pgx.Row) (*models.APIToken, error) {
	var t models.APIToken
	if err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &t.Description, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) CreateAPIToken(ctx context.Context, token *models.APIToken) error {
	var createdBy any
	err := p.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id, token_hash, description, expires_at, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		token.UserID, token.TokenHash, token.Description, token.ExpiresAt, createdBy,
	).Scan(&token.ID, &token.CreatedAt)
	if err != nil {
		return mapWriteError(err, "create api token")
	}
	return nil
}

// ValidateAPIToken looks up a token by its SHA-256 hash, returning
// models.ErrUnauthorized if not found or expired, and bumping last_used_at
// on success.
func (p *Postgres) ValidateAPIToken(ctx context.Context, tokenHash string) (*models.APIToken, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE token_hash = $1 AND (expires_at IS NULL OR expires_at > now())
		RETURNING `+tokenSelectCols, tokenHash)
	t, err := scanToken(row)
	if err != nil {
		return nil, mapReadError2(err, models.ErrUnauthorized, "validate api token")
	}
	return t, nil
}

func (p *Postgres) RevokeAPIToken(ctx context.Context, tokenID int64) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1`, tokenID)
	if err != nil {
		return mapWriteError(err, "revoke api token")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: revoke api token %d: %w", tokenID, models.ErrNotFound)
	}
	return nil
}

func (p *Postgres) ListAPITokens(ctx context.Context, userID int64) ([]*models.APIToken, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+tokenSelectCols+` FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list api tokens for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []*models.APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list api tokens for user %d: %w", userID, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
