//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/auth"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"golang.org/x/crypto/bcrypt"
)

func TestAccountServiceLoginRefreshRenameLogout(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	svc := env.accountService()

	acc := &account.Account{Username: "account-service-user", Password: "old-password"}
	if err := svc.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	if acc.Password == "old-password" {
		t.Fatalf("账号密码应该被bcrypt加密后再入库")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(acc.Password), []byte("old-password")); err != nil {
		t.Fatalf("加密后的密码无法通过bcrypt校验: %v", err)
	}

	token, refreshToken, err := svc.Login(ctx, acc.Username, "old-password")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if token == "" || refreshToken == "" {
		t.Fatalf("登录后应返回token和refreshToken")
	}
	if _, err := auth.ParseToken(token); err != nil {
		t.Fatalf("登录token无法解析: %v", err)
	}

	// 登录成功后，token和refreshToken应该同时写入Redis缓存。
	tokenBytes, err := env.cache.GetBytes(ctx, env.cache.Key("account:%d", acc.ID))
	if err != nil || string(tokenBytes) != token {
		t.Fatalf("登录token缓存异常, got=%q err=%v", string(tokenBytes), err)
	}
	refreshLookup, err := env.cache.GetBytes(ctx, env.cache.Key("refresh:%s", refreshToken))
	if err != nil || string(refreshLookup) == "" {
		t.Fatalf("refreshToken反查缓存异常, got=%q err=%v", string(refreshLookup), err)
	}

	newToken, id, username, err := svc.RefreshToken(ctx, refreshToken)
	if err != nil {
		t.Fatalf("刷新token失败: %v", err)
	}
	if newToken == "" || id != acc.ID || username != acc.Username {
		t.Fatalf("刷新token返回异常: token=%q id=%d username=%s", newToken, id, username)
	}

	renamedToken, err := svc.Rename(ctx, acc.ID, "renamed-user")
	if err != nil {
		t.Fatalf("重命名失败: %v", err)
	}
	if renamedToken == "" {
		t.Fatalf("重命名后应该返回新token")
	}
	if cached, err := env.cache.GetBytes(ctx, env.cache.Key("account:%d", acc.ID)); err != nil || string(cached) != renamedToken {
		t.Fatalf("重命名后token缓存异常, got=%q err=%v", string(cached), err)
	}

	if err := svc.ChangePassword(ctx, "renamed-user", "old-password", "new-password"); err != nil {
		t.Fatalf("修改密码失败: %v", err)
	}
	if _, _, err := svc.Login(ctx, "renamed-user", "old-password"); err == nil {
		t.Fatalf("旧密码不应该还能登录")
	}
	if _, _, err := svc.Login(ctx, "renamed-user", "new-password"); err != nil {
		t.Fatalf("新密码应该可以登录: %v", err)
	}

	if err := svc.Logout(ctx, acc.ID); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if _, err := env.cache.GetBytes(ctx, env.cache.Key("account:%d", acc.ID)); !rediscache.IsMiss(err) {
		t.Fatalf("登出后token缓存应该被删除, got=%v", err)
	}
}

func TestAccountServiceRefreshFallsBackToDBWhenRedisMiss(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	svc := env.accountService()

	acc := &account.Account{Username: "refresh-db-user", Password: "password"}
	if err := svc.CreateAccount(ctx, acc); err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	_, refreshToken, err := svc.Login(ctx, acc.Username, "password")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	// 手动删除refreshToken反查缓存，验证RefreshToken可以降级查DB。
	if err := env.cache.Delete(ctx, env.cache.Key("refresh:%s", refreshToken)); err != nil {
		t.Fatalf("删除refresh反查缓存失败: %v", err)
	}
	newToken, id, username, err := svc.RefreshToken(ctx, refreshToken)
	if err != nil {
		t.Fatalf("Redis miss时刷新token应该降级查DB: %v", err)
	}
	if newToken == "" || id != acc.ID || username != acc.Username {
		t.Fatalf("DB降级刷新token返回异常: token=%q id=%d username=%s", newToken, id, username)
	}
}
