package runtime

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

var defaultEnvironmentFiles = []string{".env", ".env.lite"}

// LoadEnvironment 加载启动目录中的环境变量文件。
// ENV_FILE 可指定文件；未指定时优先读取 .env，并在其不存在时回退到 .env.lite。
// 已存在的进程环境变量不会被文件中的同名配置覆盖。
func LoadEnvironment() error {
	if envFile := strings.TrimSpace(os.Getenv("ENV_FILE")); envFile != "" {
		return godotenv.Load(envFile)
	}

	for _, envFile := range defaultEnvironmentFiles {
		err := godotenv.Load(envFile)
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return nil
}
