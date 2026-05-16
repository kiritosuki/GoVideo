package social

import (
	"context"
	"errors"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
)

type SocialService struct {
	socialRepo  *SocialRepo
	accountRepo *account.AccountRepo
	socialMQ    *rabbitmq.SocialMQ
}

func NewSocialService(socialRepo *SocialRepo, accountRepo *account.AccountRepo, socialMQ *rabbitmq.SocialMQ) *SocialService {
	return &SocialService{
		socialRepo:  socialRepo,
		accountRepo: accountRepo,
		socialMQ:    socialMQ,
	}
}

// Follow 关注
func (s *SocialService) Follow(ctx context.Context, social *Social) error {
	// 判断follower与vlogger的账号是否存在
	_, err := s.accountRepo.FindByID(ctx, social.FollowerID)
	if err != nil {
		return err
	}
	_, err = s.accountRepo.FindByID(ctx, social.VloggerID)
	if err != nil {
		return err
	}
	// 不能关注自己
	if social.FollowerID == social.VloggerID {
		return errors.New("can not follow yourself")
	}
	// 判断是否已经关注
	isFollowed, err := s.socialRepo.IsFollowed(ctx, social)
	if err != nil {
		return err
	}
	if isFollowed {
		return errors.New("already followed")
	}
	// 向消息队列中发送关注消息
	if s.socialMQ != nil {
		if err := s.socialMQ.Follow(ctx, social.FollowerID, social.VloggerID); err == nil {
			return nil
		}
	}
	// 如果向消息队列中发送关注消息失败 手动写库
	return s.socialRepo.Follow(ctx, social)
}

// Unfollow 取消关注
func (s *SocialService) Unfollow(ctx context.Context, social *Social) error {
	// 判断follower与vlogger的账号是否存在
	_, err := s.accountRepo.FindByID(ctx, social.FollowerID)
	if err != nil {
		return err
	}
	_, err = s.accountRepo.FindByID(ctx, social.VloggerID)
	if err != nil {
		return err
	}
	// 判断是否已经关注
	isFollowed, err := s.socialRepo.IsFollowed(ctx, social)
	if err != nil {
		return err
	}
	if !isFollowed {
		return errors.New("not followed")
	}
	// 向消息队列中发送取消关注消息
	if s.socialMQ != nil {
		if err := s.socialMQ.Unfollow(ctx, social.FollowerID, social.VloggerID); err == nil {
			return nil
		}
	}
	// 如果向消息队列中发送取消关注消息失败 手动写库
	return s.socialRepo.Unfollow(ctx, social)
}

// ListAllFollowers 列出所有粉丝
func (s *SocialService) ListAllFollowers(ctx context.Context, vloggerID uint) ([]*account.Account, error) {
	// 判断vlogger用户是否存在
	_, err := s.accountRepo.FindByID(ctx, vloggerID)
	if err != nil {
		return nil, err
	}
	// 获取vlogger的所有粉丝
	return s.socialRepo.GetAllFollowers(ctx, vloggerID)
}

// CountFollowers 获取vlogger的粉丝数
func (s *SocialService) CountFollowers(ctx context.Context, vloggerID uint) (int64, error) {
	return s.socialRepo.CountFollowers(ctx, vloggerID)
}

// ListAllVloggers 列出所有关注的人
func (s *SocialService) ListAllVloggers(ctx context.Context, followerID uint) ([]*account.Account, error) {
	// 判断follower用户是否存在
	_, err := s.accountRepo.FindByID(ctx, followerID)
	if err != nil {
		return nil, err
	}
	// 获取follower的所有关注的人
	return s.socialRepo.GetAllVloggers(ctx, followerID)
}

// CountVloggers 获取follower的关注数
func (s *SocialService) CountVloggers(ctx context.Context, followerID uint) (int64, error) {
	return s.socialRepo.CountVloggers(ctx, followerID)
}
