package types

import (
	"os"
	"testing"
)

// TestMain 统一在测试环境启用凭证加密。凭证加密是严格模式（无 SYSTEM_AES_KEY 时
// 拒绝把明文凭证写入数据库），因此大多数测试需要在"有 key"下运行；需要专门验证
// "无 key"行为的测试用 t.Setenv 覆盖即可。
func TestMain(m *testing.M) {
	if os.Getenv("SYSTEM_AES_KEY") == "" {
		_ = os.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	}
	os.Exit(m.Run())
}
