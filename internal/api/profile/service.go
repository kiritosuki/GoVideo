package profile

import (
	"context"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

type ProfileService struct {
	accountRepo *account.AccountRepo
	videoRepo   *video.VideoRepo
	socialRepo  *social.SocialRepo
	cache       *rediscache.Client
}

func NewProfileService(accountRepo *account.AccountRepo, videoRepo *video.VideoRepo, socialRepo *social.SocialRepo, cache *rediscache.Client) *ProfileService {
	return &ProfileService{
		accountRepo: accountRepo,
		videoRepo:   videoRepo,
		socialRepo:  socialRepo,
		cache:       cache,
	}
}

func (s *ProfileService) GetAccountProfile(ctx context.Context, accountID uint) (*GetAccountProfileResponse, error) {
	acc, err := s.accountRepo.FindByID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	videoCount, _ := s.videoRepo.CountByAuthorID(ctx, accountID)
	totalLikes, _ := s.videoRepo.TotalLikesByAuthorID(ctx, accountID)
	followerCount, _ := s.socialRepo.CountFollowers(ctx, accountID)
	vloggerCount, _ := s.socialRepo.CountVloggers(ctx, accountID)

	return &GetAccountProfileResponse{
		Account: account.FindByIDResponse{
			ID:        acc.ID,
			Username:  acc.Username,
			AvatarURL: acc.AvatarURL,
			Bio:       acc.Bio,
		},
		VideoCount:    videoCount,
		TotalLikes:    totalLikes,
		FollowerCount: followerCount,
		VloggerCount:  vloggerCount,
	}, nil
}
