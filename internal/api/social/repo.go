package social

import (
	"context"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"gorm.io/gorm"
)

type SocialRepo struct {
	db *gorm.DB
}

func NewSocialRepo(db *gorm.DB) *SocialRepo {
	return &SocialRepo{
		db: db,
	}
}

// IsFollowed 判断follower是否已经关注vlogger
func (r *SocialRepo) IsFollowed(ctx context.Context, social *Social) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("follower_id = ? and vlogger_id = ?", social.FollowerID, social.VloggerID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// Follow 向数据库插入一条关注记录
func (r *SocialRepo) Follow(ctx context.Context, social *Social) error {
	return r.db.WithContext(ctx).Create(social).Error
}

// Unfollow 删除数据库中的关注记录
func (r *SocialRepo) Unfollow(ctx context.Context, social *Social) error {
	return r.db.WithContext(ctx).
		Where("follower_id = ? and vlogger_id = ?", social.FollowerID, social.VloggerID).
		Delete(&Social{}).Error
}

// GetAllFollowers 获取vlogger的所有粉丝
func (r *SocialRepo) GetAllFollowers(ctx context.Context, vloggerID uint) ([]*account.Account, error) {
	// 查询socials
	var socials []Social
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("vlogger_id = ?", vloggerID).
		Limit(200).
		Find(&socials).Error; err != nil {
		return nil, err
	}
	// 从socials中提取followerID
	followerIDs := make([]uint, 0, len(socials))
	for _, social := range socials {
		followerIDs = append(followerIDs, social.FollowerID)
	}
	if len(followerIDs) == 0 {
		return []*account.Account{}, nil
	}
	// 查询followers
	var followers []*account.Account
	if err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Where("id in ?", followerIDs).
		Find(&followers).Error; err != nil {
		return nil, err
	}
	return followers, nil
}

// CountFollowers 获取vlogger的粉丝数
func (r *SocialRepo) CountFollowers(ctx context.Context, vloggerID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("vlogger_id = ?", vloggerID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// GetAllVloggers 获取follower的所有关注的人
func (r *SocialRepo) GetAllVloggers(ctx context.Context, followerID uint) ([]*account.Account, error) {
	// 查询socials
	var socials []Social
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("follower_id = ?", followerID).
		Limit(200).
		Find(&socials).Error; err != nil {
		return nil, err
	}
	// 从socials中提取vloggerID
	vloggerIDs := make([]uint, 0, len(socials))
	for _, social := range socials {
		vloggerIDs = append(vloggerIDs, social.VloggerID)
	}
	if len(vloggerIDs) == 0 {
		return []*account.Account{}, nil
	}
	// 查询vloggers
	var vloggers []*account.Account
	if err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Where("id in ?", vloggerIDs).
		Find(&vloggers).Error; err != nil {
		return nil, err
	}
	return vloggers, nil
}

// CountVloggers 获取follower的关注数
func (r *SocialRepo) CountVloggers(ctx context.Context, followerID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("follower_id = ?", followerID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
