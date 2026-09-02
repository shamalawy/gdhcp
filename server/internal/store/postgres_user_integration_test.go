package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// testPool opens the scratch database named by GDHCP_TEST_DSN, or skips.
func testPool(t *testing.T) (*pgxpool.Pool, Store) {
	t.Helper()
	dsn := os.Getenv("GDHCP_TEST_DSN")
	if dsn == "" {
		t.Skip("set GDHCP_TEST_DSN to run store integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, NewPostgres(pool)
}

// TestPostgresUpdateUserPersistsRoleAndUsername is the regression guard for a
// silent data-loss defect: the API handler validated a new role, assigned it,
// and returned 200 echoing it, while UpdateUser's SQL never wrote it. Nothing
// failed — promoting or demoting a user through the API simply did nothing.
//
// Only a test against real SQL catches this. The in-memory Mock replaces the
// whole user struct, so it persisted the fields Postgres dropped and every
// mock-backed test passed throughout.
func TestPostgresUpdateUserPersistsRoleAndUsername(t *testing.T) {
	_, st := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	original := &models.User{
		Username:     fixtureName("upduser", suffix),
		Email:        fixtureName("upduser", suffix) + "@example.test",
		PasswordHash: "$2a$10$notarealhashnotarealhashnotarealhashnotarealhashno",
		Role:         models.RoleViewer,
		IsActive:     true,
	}
	if err := st.CreateUser(ctx, original); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		if err := st.DeleteUser(ctx, original.ID); err != nil {
			t.Errorf("cleanup delete user %d: %v", original.ID, err)
		}
	})

	renamed := fixtureName("upduser-renamed", suffix)
	original.Username = renamed
	original.Role = models.RoleAdmin
	original.IsActive = false
	if err := st.UpdateUser(ctx, original); err != nil {
		t.Fatalf("update user: %v", err)
	}

	// Re-read from the database rather than trusting the in-memory struct —
	// believing the echoed value is exactly what hid this defect.
	got, err := st.GetUserByID(ctx, original.ID)
	if err != nil {
		t.Fatalf("re-read user: %v", err)
	}
	if got.Role != models.RoleAdmin {
		t.Errorf("role = %q, want %q; the role change was not persisted", got.Role, models.RoleAdmin)
	}
	if got.Username != renamed {
		t.Errorf("username = %q, want %q; the rename was not persisted", got.Username, renamed)
	}
	if got.IsActive {
		t.Error("is_active = true, want false")
	}
	if got.PasswordHash != original.PasswordHash {
		t.Error("UpdateUser must not touch password_hash")
	}
}

// TestPostgresUpdateUserRejectsInvalidRole: the column has a CHECK
// constraint, so an invalid role would otherwise surface as a raw constraint
// error rather than ErrInvalidInput.
func TestPostgresUpdateUserRejectsInvalidRole(t *testing.T) {
	_, st := testPool(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	u := &models.User{
		Username:     fixtureName("badrole", suffix),
		Email:        fixtureName("badrole", suffix) + "@example.test",
		PasswordHash: "$2a$10$notarealhashnotarealhashnotarealhashnotarealhashno",
		Role:         models.RoleViewer,
		IsActive:     true,
	}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(ctx, u.ID) })

	u.Role = models.Role("superuser")
	err := st.UpdateUser(ctx, u)
	if !errors.Is(err, models.ErrInvalidInput) {
		t.Fatalf("UpdateUser with an invalid role returned %v, want ErrInvalidInput", err)
	}
}

func fixtureName(prefix string, suffix int64) string {
	return prefix + "-" + time.Unix(0, suffix).UTC().Format("150405.000000000")
}
