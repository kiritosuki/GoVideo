package social

type Social struct {
	ID         uint `gorm:"primaryKey"`
	FollowerID uint `gorm:"not null;index;uniqueIndex:idx_socials_follower_vlogger,priority:1"`
	VloggerID  uint `gorm:"not null;index;uniqueIndex:idx_socials_follower_vlogger,priority:2"`
}
