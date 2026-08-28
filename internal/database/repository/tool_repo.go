package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

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

// Borrow transfers ownership of use of a tool to the given borrower. It returns
// an error when the tool is not found or is already borrowed.
func (r *ToolRepository) Borrow(ctx context.Context, toolID, borrowerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		if err := tx.Where("id = ? AND borrower_id IS NULL", toolID).First(&tool).Error; err != nil {
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

// BorrowByName is Borrow by tool name (case-insensitive).
func (r *ToolRepository) BorrowByName(ctx context.Context, name, borrowerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		if err := tx.Where("LOWER(name) = ? AND borrower_id IS NULL", strings.ToLower(name)).First(&tool).Error; err != nil {
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

// Return clears the borrower from a tool.
func (r *ToolRepository) Return(ctx context.Context, toolID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		if err := tx.Where("id = ? AND borrower_id IS NOT NULL", toolID).First(&tool).Error; err != nil {
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

// ReturnByName is Return by tool name (case-insensitive).
func (r *ToolRepository) ReturnByName(ctx context.Context, name string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tool database.Tool
		if err := tx.Where("LOWER(name) = ? AND borrower_id IS NOT NULL", strings.ToLower(name)).First(&tool).Error; err != nil {
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
