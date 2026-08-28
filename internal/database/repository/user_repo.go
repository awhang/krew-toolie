package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"krew-toolie/internal/database"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// GetOrCreate returns the user identified by discordID, creating it if it does
// not yet exist.
func (r *UserRepository) GetOrCreate(ctx context.Context, discordID, username string) (*database.User, error) {
	var user database.User
	err := r.db.WithContext(ctx).
		Where("discord_id = ?", discordID).
		First(&user).Error

	if err == nil {
		return &user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	user = database.User{
		DiscordID: discordID,
		Username:  username,
	}
	if err := r.db.WithContext(ctx).Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
