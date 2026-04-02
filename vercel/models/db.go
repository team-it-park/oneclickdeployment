package models

type UserDBModel struct {
	Email        string `gorm:"column:email;primaryKey"`
	Username     string `gorm:"column:username"`
	PasswordHash string `gorm:"column:password_hash"`
	GithubAccess bool   `gorm:"column:github_access"`
}
