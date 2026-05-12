package like

type LikeHandler struct {
	likeService *LikeService
}

func NewLikeHandler(likeService *LikeService) *LikeHandler {
	return &LikeHandler{
		likeService: likeService,
	}
}
