package tag

type Tag struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;type:varchar(100);not null" json:"name"`
}

type VideoTag struct {
	ID      uint `gorm:"primaryKey"`
	VideoID uint `gorm:"index;not null"`
	TagID   uint `gorm:"index;not null"`
}
