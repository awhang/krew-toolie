package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"krew-toolie/internal/database"
	"krew-toolie/internal/fuzzy"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// GetOrCreate returns the user identified by discordID, creating it if it does
// not yet exist, and refreshing the stored display names when they change.
func (r *UserRepository) GetOrCreate(ctx context.Context, discordID, username, globalName, serverName string) (*database.User, error) {
	var user database.User
	err := r.db.WithContext(ctx).
		Where("discord_id = ?", discordID).
		First(&user).Error

	if err == nil {
		// Refresh display names on every command so server/global names stay current.
		if user.Username != username || user.GlobalName != globalName || user.ServerName != serverName {
			user.Username = username
			user.GlobalName = globalName
			user.ServerName = serverName
			if upErr := r.db.WithContext(ctx).Save(&user).Error; upErr != nil {
				return nil, upErr
			}
		}
		return &user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	user = database.User{
		DiscordID:  discordID,
		Username:   username,
		GlobalName: globalName,
		ServerName: serverName,
	}
	if err := r.db.WithContext(ctx).Create(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByOwnerFuzzy returns users whose username, global display name, or
// server nickname fuzzy-match query using token-substring, case-insensitive
// matching (see internal/fuzzy). An empty query returns no matches.
func (r *UserRepository) FindByOwnerFuzzy(ctx context.Context, query string) ([]database.User, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	var users []database.User
	if err := r.db.WithContext(ctx).Find(&users).Error; err != nil {
		return nil, err
	}

	matches := make([]database.User, 0, len(users))
	for _, u := range users {
		if fuzzy.Match(query, u.Username) ||
			fuzzy.Match(query, u.GlobalName) ||
			fuzzy.Match(query, u.ServerName) {
			matches = append(matches, u)
		}
	}
	return matches, nil
}
