package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 测试可以从yaml文件中加载完整配置
// 这里使用临时文件模拟配置文件 不依赖项目中的真实config-dev.yaml
func TestLoadConfigFromYAML(t *testing.T) {
	configFile := writeTempConfig(t)

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Fatalf("expected server port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Database.Host != "localhost" || cfg.Database.DBName != "go_video_test" {
		t.Fatalf("unexpected database config: %+v", cfg.Database)
	}
	if cfg.JWT.JWTSecret != "file-secret" {
		t.Fatalf("expected jwt secret from file, got %q", cfg.JWT.JWTSecret)
	}
}

// 测试配置文件不存在时会返回错误
// 这个测试用于保证启动入口能通过Load的错误发现错误CONFIG_PATH
func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing config file to return error")
	}
}

// 测试环境变量可以覆盖配置文件中的值
// 项目配置约定是先读yaml 再用环境变量覆盖 因此这里重点验证覆盖逻辑是否生效
func TestApplyEnvOverrides(t *testing.T) {
	configFile := writeTempConfig(t)
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("MYSQL_HOST", "mysql.internal")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("RABBITMQ_USER", "admin")
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("COS_TIMEOUT_SECONDS", "600")

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Fatalf("expected env server port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Database.Host != "mysql.internal" {
		t.Fatalf("expected env mysql host, got %q", cfg.Database.Host)
	}
	if cfg.Redis.DB != 3 {
		t.Fatalf("expected env redis db 3, got %d", cfg.Redis.DB)
	}
	if cfg.RabbitMQ.Username != "admin" {
		t.Fatalf("expected env rabbitmq user admin, got %q", cfg.RabbitMQ.Username)
	}
	if cfg.JWT.JWTSecret != "env-secret" {
		t.Fatalf("expected env jwt secret, got %q", cfg.JWT.JWTSecret)
	}
	if cfg.COS.TimeoutSeconds != 600 {
		t.Fatalf("expected env cos timeout 600, got %d", cfg.COS.TimeoutSeconds)
	}
}

// 测试非法数字环境变量不会覆盖原配置
// 例如端口或db编号写错时 当前逻辑会忽略该环境变量 保留配置文件中的值
func TestApplyEnvOverridesIgnoresInvalidNumbers(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Port: 8080},
		Redis:  RedisConfig{DB: 1},
		COS:    COSConfig{TimeoutSeconds: 300},
	}
	t.Setenv("SERVER_PORT", "not-number")
	t.Setenv("REDIS_DB", "not-number")
	t.Setenv("COS_TIMEOUT_SECONDS", "not-number")

	ApplyEnvOverrides(&cfg)

	if cfg.Server.Port != 8080 {
		t.Fatalf("expected server port to keep original value, got %d", cfg.Server.Port)
	}
	if cfg.Redis.DB != 1 {
		t.Fatalf("expected redis db to keep original value, got %d", cfg.Redis.DB)
	}
	if cfg.COS.TimeoutSeconds != 300 {
		t.Fatalf("expected cos timeout to keep original value, got %d", cfg.COS.TimeoutSeconds)
	}
}

// 测试传入nil时不会panic
// 这个测试覆盖ApplyEnvOverrides的防御性分支
func TestApplyEnvOverridesNilConfig(t *testing.T) {
	ApplyEnvOverrides(nil)
}

func writeTempConfig(t *testing.T) string {
	t.Helper()

	content := []byte(`
server:
  port: 8080
database:
  host: localhost
  port: 3306
  user: root
  password: password
  dbname: go_video_test
redis:
  host: localhost
  port: 6379
  password:
  db: 0
rabbitmq:
  host: localhost
  port: 5672
  username: guest
  password: guest
jwt:
  jwt_secret: file-secret
cos:
  bucket: examplebucket-1250000000
  region: ap-beijing
  secret_id: sid
  secret_key: skey
  public_base_url:
  timeout_seconds: 300
observability:
  pprof:
    enabled: true
    api_addr: localhost:6060
    worker_addr: localhost:6061
`)
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return file
}
