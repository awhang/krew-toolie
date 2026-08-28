package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"krew-toolie/internal/database"
)

type ToolRepository struct {
	db *gorm.DB
}

func NewToolRepository(db *gorm.DB) *ToolRepository {
	return &ToolRepository{db: db}
}

func (r *ToolRepository) Create(ctx context.Context, tool *database.Tool) error {
	return r.db.WithContext(ctx).Create(tool).Error
}

func (r *ToolRepository) GetByID(ctx context.Context, id string) (*database.Tool, error) {
	var tool database.Tool
	err := r.db.WithContext(ctx).
		Preload("Owner").
		Preload("Borrower").
		First(&tool, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &tool, err
}

func (r *ToolRepository) GetByOwner(ctx context.Context, ownerID string) ([]database.Tool, error) {
	var tools []database.Tool
	err := r.db.WithContext(ctx).
		Preload("Borrower").
		Where("owner_id = ?", ownerID).
		Find(&tools).Error
	return tools, err
}

func (r *ToolRepository) GetAvailable(ctx context.Context) ([]database.Tool, error) {
	var tools []database.Tool
	err := r.db.WithContext(ctx).
		Preload("Owner").
		Preload("Borrower").
		Where("borrower_id IS NULL").
		Find(&tools).Error
	return tools, err
}

// Borrow transfers the use of a tool to the given borrower. The row is locked
// with FOR UPDATE inside the transaction so concurrent borrow attempts cannot
// both succeed. It returns an error when the tool is not found or is already
// borrowed.
func (r *ToolRepository) Borrow(ctx context.Context, toolID, borrowerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND borrower_id IS NULL", toolID).
			First(&tool).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("tool not found or already borrowed")
			}
			return err
		}

		now := time.Now()
		return tx.Model(&database.Tool{}).
			Where("id = ?", toolID).
			Updates(map[string]interface{}{
				"borrower_id": borrowerID,
				"borrowed_at": &now,
			}).Error
	})
}

// BorrowByName is Borrow by tool name (case-insensitive exact name match).
// Prefer Borrow with a resolved tool ID when the caller has already identified
// the exact tool.
func (r *ToolRepository) BorrowByName(ctx context.Context, name, borrowerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("LOWER(name) = ? AND borrower_id IS NULL", strings.ToLower(name)).
			First(&tool).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("tool not found or already borrowed")
			}
			return err
		}

		now := time.Now()
		return tx.Model(&database.Tool{}).
			Where("id = ?", tool.ID).
			Updates(map[string]interface{}{
				"borrower_id": borrowerID,
				"borrowed_at": &now,
			}).Error
	})
}

// Return clears the borrower from a tool. The row is locked with FOR UPDATE to
// keep concurrent return/borrow operations consistent.
func (r *ToolRepository) Return(ctx context.Context, toolID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND borrower_id IS NOT NULL", toolID).
			First(&tool).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("tool not found or not borrowed")
			}
			return err
		}

		return tx.Model(&database.Tool{}).
			Where("id = ?", toolID).
			Updates(map[string]interface{}{
				"borrower_id": nil,
				"borrowed_at": nil,
			}).Error
	})
}

// ReturnByName is Return by tool name (case-insensitive exact name match).
func (r *ToolRepository) ReturnByName(ctx context.Context, name string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("LOWER(name) = ? AND borrower_id IS NOT NULL", strings.ToLower(name)).
			First(&tool).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("tool not found or not borrowed")
			}
			return err
		}

		return tx.Model(&database.Tool{}).
			Where("id = ?", tool.ID).
			Updates(map[string]interface{}{
				"borrower_id": nil,
				"borrowed_at": nil,
			}).Error
	})
}

// Remove deletes a tool owned by ownerID, ensuring a user can only remove their
// own tools.
func (r *ToolRepository) Remove(ctx context.Context, toolID, ownerID string) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND owner_id = ?", toolID, ownerID).
		Delete(&database.Tool{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("tool not found or not owned by you")
	}
	return nil
}

// RemoveByName deletes a tool owned by ownerID matching name (case-insensitive
// exact name match). Callers should have already resolved a fuzzy match to a
// specific name when multiple candidates exist.
func (r *ToolRepository) RemoveByName(ctx context.Context, name, ownerID string) error {
	res := r.db.WithContext(ctx).
		Where("LOWER(name) = ? AND owner_id = ?", strings.ToLower(name), ownerID).
		Delete(&database.Tool{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("tool not found or not owned by you")
	}
	return nil
}

// ExistsByName reports whether the owner already has a tool with name
// (case-insensitive exact match).
func (r *ToolRepository) ExistsByName(ctx context.Context, name, ownerID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&database.Tool{}).
		Where("LOWER(name) = ? AND owner_id = ?", strings.ToLower(name), ownerID).
		Count(&count).Error
	return count > 0, err
}
