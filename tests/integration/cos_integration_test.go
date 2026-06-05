//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/config"
	cosstore "github.com/kiritosuki/GoVideo/internal/middleware/cos"
)

func TestCOSUploadFileAndDeleteObject(t *testing.T) {
	cfg := loadIntegrationConfigForCOS(t)
	if cfg.COS.Bucket == "" || cfg.COS.Region == "" || cfg.COS.SecretID == "" || cfg.COS.SecretKey == "" {
		t.Skip("COS配置不完整，跳过真实对象存储集成测试")
	}
	if cfg.COS.SecretID == "change-me" || cfg.COS.SecretKey == "change-me" {
		t.Skip("COS配置仍为占位值，跳过真实对象存储集成测试")
	}

	client, err := cosstore.NewClient(&cfg.COS)
	if err != nil {
		t.Fatalf("创建COS客户端失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.COS.TimeoutSeconds)*time.Second)
	defer cancel()

	// 只上传一个很小的文本文件，验证真实上传、URL生成和删除能力，避免对COS产生过多请求。
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "cos-integration.txt")
	if err := os.WriteFile(localPath, []byte("govideo cos integration test\n"), 0644); err != nil {
		t.Fatalf("创建本地临时文件失败: %v", err)
	}

	objectKey := "integration-tests/cos-" + time.Now().UTC().Format("20060102150405.000000000") + ".txt"
	objectURL, err := client.UploadFile(ctx, objectKey, localPath)
	if err != nil {
		t.Fatalf("上传文件到COS失败: %v", err)
	}
	deleted := false
	defer func() {
		if deleted {
			return
		}
		if err := client.DeleteObject(context.Background(), objectKey); err != nil {
			t.Logf("清理COS测试对象失败: key=%s err=%v", objectKey, err)
		}
	}()

	if objectURL == "" {
		t.Fatalf("上传成功后应返回对象URL")
	}
	if !strings.Contains(objectURL, objectKey) {
		t.Fatalf("对象URL应包含对象key, url=%s key=%s", objectURL, objectKey)
	}
	if cfg.COS.PublicBaseURL != "" && !strings.HasPrefix(objectURL, strings.TrimRight(cfg.COS.PublicBaseURL, "/")+"/") {
		t.Fatalf("对象URL应使用public_base_url前缀, got=%s", objectURL)
	}

	if err := client.DeleteObject(ctx, objectKey); err != nil {
		t.Fatalf("删除COS测试对象失败: %v", err)
	}
	deleted = true
}

func loadIntegrationConfigForCOS(t *testing.T) config.Config {
	t.Helper()

	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "configs/config-integration.yaml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("加载集成测试配置失败: %v", err)
	}
	if cfg.COS.TimeoutSeconds <= 0 {
		cfg.COS.TimeoutSeconds = 300
	}
	return cfg
}
