package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"krew-toolie/internal/database"
)

// testDB returns an in-memory SQLite database with tables matching the
// production schema. The production models rely on Postgres-specific UUID
// defaults (gen_random_uuid), which SQLite does not support, so tables are
// created manually here for tests.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Keep a single connection so the in-memory database is not re-created.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	schema := `
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    discord_id TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE tools (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    store_link TEXT,
    owner_id TEXT NOT NULL,
    borrower_id TEXT,
    borrowed_at DATETIME,
    created_at DATETIME,
    updated_at DATETIME,
    UNIQUE(owner_id, name)
);
`
	if err := db.Exec(schema).Error; err != nil {
		t.Fatalf("failed to create test schema: %v", err)
	}
	return db
}

func mustCreateUser(t *testing.T, db *gorm.DB, discordID, username string) *database.User {
	t.Helper()

	user := &database.User{
		ID:        uuid.NewString(),
		DiscordID: discordID,
		Username:  username,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return user
}

func TestGetOrCreate_CreatesNewUser(t *testing.T) {
	db := testDB(t)
	repo := NewUserRepository(db)

	user, err := repo.GetOrCreate(context.Background(), "discord-1", "alice")
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if user.DiscordID != "discord-1" {
		t.Errorf("expected discord_id=%q, got %q", "discord-1", user.DiscordID)
	}
	if user.Username != "alice" {
		t.Errorf("expected username %q, got %q", "alice", user.Username)
	}

	// Calling again should return the existing user, not create a duplicate.
	count := int64(0)
	if err := db.Model(&database.User{}).Where("discord_id = ?", "discord-1").Count(&count).Error; err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user, got %d", count)
	}
}

func TestGetOrCreate_ReturnsExistingUser(t *testing.T) {
	db := testDB(t)
	repo := NewUserRepository(db)

	existing := mustCreateUser(t, db, "discord-2", "bob")

	user, err := repo.GetOrCreate(context.Background(), "discord-2", "bob-renamed")
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if user.ID != existing.ID {
		t.Errorf("expected existing user ID %q, got %q", existing.ID, user.ID)
	}
}

func TestToolBorrow_Success(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")
	borrower := mustCreateUser(t, db, "borrower-1", "borrower")

	tool := &database.Tool{
		ID:      uuid.NewString(),
		Name:    "Drill",
		OwnerID: owner.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)
	if err := repo.BorrowByName(context.Background(), "drill", borrower.ID); err != nil {
		t.Fatalf("BorrowByName returned error: %v", err)
	}

	var updated database.Tool
	if err := db.First(&updated, "id = ?", tool.ID).Error; err != nil {
		t.Fatalf("load tool failed: %v", err)
	}
	if updated.BorrowerID == nil || *updated.BorrowerID != borrower.ID {
		t.Errorf("expected borrower id %q, got %v", borrower.ID, updated.BorrowerID)
	}
	if updated.BorrowedAt == nil {
		t.Error("expected borrowed_at to be set")
	}
}

func TestToolBorrow_AlreadyBorrowedFails(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")
	borrower := mustCreateUser(t, db, "borrower-1", "borrower")

	tool := &database.Tool{
		ID:         uuid.NewString(),
		Name:       "Hammer",
		OwnerID:    owner.ID,
		BorrowerID: &borrower.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)
	if err := repo.BorrowByName(context.Background(), "hammer", borrower.ID); err == nil {
		t.Fatal("expected error when borrowing an already-borrowed tool")
	}
}

func TestToolReturn_Success(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")
	borrower := mustCreateUser(t, db, "borrower-1", "borrower")

	tool := &database.Tool{
		ID:         uuid.NewString(),
		Name:       "Wrench",
		OwnerID:    owner.ID,
		BorrowerID: &borrower.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)
	if err := repo.ReturnByName(context.Background(), "wrench"); err != nil {
		t.Fatalf("ReturnByName returned error: %v", err)
	}

	var updated database.Tool
	if err := db.First(&updated, "id = ?", tool.ID).Error; err != nil {
		t.Fatalf("load tool failed: %v", err)
	}
	if updated.BorrowerID != nil {
		t.Errorf("expected borrower_id to be nil, got %v", *updated.BorrowerID)
	}
}

func TestToolReturn_NotBorrowedFails(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")

	tool := &database.Tool{
		ID:      uuid.NewString(),
		Name:    "Saw",
		OwnerID: owner.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)
	if err := repo.ReturnByName(context.Background(), "saw"); err == nil {
		t.Fatal("expected error when returning a tool that is not borrowed")
	}
}

func TestToolRemove_OwnerScoped(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")
	other := mustCreateUser(t, db, "other-1", "other")

	tool := &database.Tool{
		ID:      uuid.NewString(),
		Name:    "Drill",
		OwnerID: owner.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)

	// A different owner cannot remove this tool.
	if err := repo.Remove(context.Background(), tool.ID, other.ID); err == nil {
		t.Fatal("expected error when a non-owner removes a tool")
	}

	// The owner can remove the tool.
	if err := repo.Remove(context.Background(), tool.ID, owner.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	var count int64
	if err := db.Model(&database.Tool{}).Where("id = ?", tool.ID).Count(&count).Error; err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected tool to be deleted, count=%d", count)
	}
}

func TestToolRemoveByName_OwnerScoped(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")

	tool := &database.Tool{
		ID:      uuid.NewString(),
		Name:    "Hammer",
		OwnerID: owner.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)
	if err := repo.RemoveByName(context.Background(), "HAMMER", owner.ID); err != nil {
		t.Fatalf("RemoveByName returned error: %v", err)
	}

	var count int64
	if err := db.Model(&database.Tool{}).Where("id = ?", tool.ID).Count(&count).Error; err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected tool to be deleted, count=%d", count)
	}
}

func TestToolExistsByName(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")
	other := mustCreateUser(t, db, "other-1", "other")

	tool := &database.Tool{
		ID:      uuid.NewString(),
		Name:    "Wrench",
		OwnerID: owner.ID,
	}
	if err := db.Create(tool).Error; err != nil {
		t.Fatalf("create tool failed: %v", err)
	}

	repo := NewToolRepository(db)

	exists, err := repo.ExistsByName(context.Background(), "wrench", owner.ID)
	if err != nil {
		t.Fatalf("ExistsByName returned error: %v", err)
	}
	if !exists {
		t.Error("expected tool to exist for owner")
	}

	// Same name owned by a different user should not count.
	exists, err = repo.ExistsByName(context.Background(), "wrench", other.ID)
	if err != nil {
		t.Fatalf("ExistsByName returned error: %v", err)
	}
	if exists {
		t.Error("expected tool NOT to exist for other owner")
	}
}

func TestToolDuplicateNamePerOwnerRejectedByUniqueIndex(t *testing.T) {
	db := testDB(t)
	owner := mustCreateUser(t, db, "owner-1", "owner")

	first := &database.Tool{ID: uuid.NewString(), Name: "Drill", OwnerID: owner.ID}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first tool failed: %v", err)
	}

	dup := &database.Tool{ID: uuid.NewString(), Name: "Drill", OwnerID: owner.ID}
	if err := db.Create(dup).Error; err == nil {
		t.Fatal("expected unique index to reject duplicate exact name per owner")
	}
}

func TestFindByUsernameFuzzy(t *testing.T) {
	db := testDB(t)
	mustCreateUser(t, db, "u1", "Alice Johnson")
	mustCreateUser(t, db, "u2", "Bob Smith")

	repo := NewUserRepository(db)

	users, err := repo.FindByUsernameFuzzy(context.Background(), "john")
	if err != nil {
		t.Fatalf("FindByUsernameFuzzy returned error: %v", err)
	}
	if len(users) != 1 || users[0].Username != "Alice Johnson" {
		t.Errorf("expected 1 match 'Alice Johnson', got %+v", users)
	}

	// Case-insensitive, token substring.
	users, err = repo.FindByUsernameFuzzy(context.Background(), "SMITH")
	if err != nil {
		t.Fatalf("FindByUsernameFuzzy returned error: %v", err)
	}
	if len(users) != 1 || users[0].Username != "Bob Smith" {
		t.Errorf("expected 1 match 'Bob Smith', got %+v", users)
	}

	// Empty query -> no matches.
	users, err = repo.FindByUsernameFuzzy(context.Background(), "")
	if err != nil {
		t.Fatalf("FindByUsernameFuzzy returned error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 matches for empty query, got %d", len(users))
	}
}
