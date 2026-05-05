package account

type Account struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"unique" json:"username"`
	Password     string `json:"-"`
	Token        string `json:"-"`
	RefreshToken string `json:"-"`
	AvatarURL    string `gorm:"type:varchar(512)" json:"avatar_url,omitempty"`
	Bio          string `gorm:"type:varchar(255)" json:"bio,omitempty"`
}
