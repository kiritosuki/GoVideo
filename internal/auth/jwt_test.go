package auth

import "testing"

// resetJWTSecretForTest 重置全局签名密钥
// JWT模块内部使用全局变量保存签名密钥 因此每个测试前都需要重置 避免测试之间相互影响
func resetJWTSecretForTest() {
	jwtSigningSecret = nil
}

// 测试空JWT密钥会被拒绝
// 这个测试用于保证启动时如果配置文件没有提供jwt_secret 服务会明确失败 而不是使用空密钥签发token
func TestSetJWTSecretRejectsEmptySecret(t *testing.T) {
	resetJWTSecretForTest()

	if err := SetJWTSecret("   "); err == nil {
		t.Fatal("expected empty jwt secret to be rejected")
	}
}

// 测试JWT密钥未初始化时不能生成token
// 这个测试用于保证业务代码必须先完成配置初始化 再调用GenerateToken
func TestGenerateTokenRequiresInitializedSecret(t *testing.T) {
	resetJWTSecretForTest()

	if _, err := GenerateToken(1, "alice"); err == nil {
		t.Fatal("expected GenerateToken to fail before jwt secret is initialized")
	}
}

// 测试设置JWT密钥后 可以正常生成并解析token
// 这里验证的是JWT模块最核心的闭环: GenerateToken签发的信息能被ParseToken解析出来
func TestGenerateAndParseToken(t *testing.T) {
	resetJWTSecretForTest()
	if err := SetJWTSecret("test-secret"); err != nil {
		t.Fatalf("set jwt secret: %v", err)
	}

	token, err := GenerateToken(7, "alice")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}

	if claims.AccountID != 7 || claims.Username != "alice" {
		t.Fatalf("unexpected claims: accountID=%d username=%s", claims.AccountID, claims.Username)
	}
}

// 测试非法token无法解析
// 这个测试用于覆盖请求中传入伪造token时的错误路径
func TestParseInvalidToken(t *testing.T) {
	resetJWTSecretForTest()
	if err := SetJWTSecret("test-secret"); err != nil {
		t.Fatalf("set jwt secret: %v", err)
	}

	if _, err := ParseToken("not-a-jwt-token"); err == nil {
		t.Fatal("expected invalid token to be rejected")
	}
}

// 测试更换JWT密钥后 旧token会失效
// 分布式部署时所有节点必须使用相同jwt_secret 这个测试能体现密钥不一致会导致token无法互认
func TestTokenInvalidAfterSecretChanged(t *testing.T) {
	resetJWTSecretForTest()
	if err := SetJWTSecret("old-secret"); err != nil {
		t.Fatalf("set old secret: %v", err)
	}
	token, err := GenerateToken(1, "alice")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	if err := SetJWTSecret("new-secret"); err != nil {
		t.Fatalf("set new secret: %v", err)
	}
	if _, err := ParseToken(token); err == nil {
		t.Fatal("expected old token to be rejected after jwt secret changed")
	}
}
