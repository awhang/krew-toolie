package database

import (
	"time"
)

type User struct {
	ID        string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`
	DiscordID string    `gorm:"uniqueIndex;not null" json:"discord_id"`
	Username  string    `gorm:"not null" json:"username"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	// Relationships
	ToolsOwned []Tool `gorm:"foreignKey:OwnerID" json:"tools_owned,omitempty"`
}

type Tool struct {
	ID         string     `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`
	Name       string     `gorm:"not null" json:"name"`
	StoreLink  *string    `gorm:"column:store_link" json:"store_link,omitempty"` // Optional
	OwnerID    string     `gorm:"not null;type:uuid" json:"owner_id"`
	BorrowerID *string    `gorm:"column:borrower_id;type:uuid;index" json:"borrower_id,omitempty"`
	BorrowedAt *time.Time `gorm:"column:borrowed_at" json:"borrowed_at,omitempty"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`

	// Relationships
	Owner    User  `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Borrower *User `gorm:"foreignKey:BorrowerID" json:"borrower,omitempty"`
}
