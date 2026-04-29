package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// appLogger 使用私有实例，避免外部依赖改写 logrus 全局状态导致日志丢失
var appLogger = logrus.New()

var (
	loggerMu      sync.Mutex
	activeLogFile io.WriteCloser
)

// LogLevel 日志级别类型
type LogLevel string

// 日志级别常量
const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
)

// ANSI颜色代码
const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
	colorReset  = "\033[0m"
)

type CustomFormatter struct {
	ForceColor bool // 是否强制使用颜色，即使在非终端环境下
}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	timestamp := entry.Time.Format("2006-01-02 15:04:05.000")
	level := strings.ToUpper(entry.Level.String())

	// 根据日志级别设置颜色
	var levelColor, resetColor string
	if f.ForceColor {
		switch entry.Level {
		case logrus.DebugLevel:
			levelColor = colorCyan
		case logrus.InfoLevel:
			levelColor = colorGreen
		case logrus.WarnLevel:
			levelColor = colorYellow
		case logrus.ErrorLevel:
			levelColor = colorRed
		case logrus.FatalLevel:
			levelColor = colorPurple
		default:
			levelColor = colorReset
		}
		resetColor = colorReset
	}

	// 取出 caller 字段
	caller := ""
	if val, ok := entry.Data["caller"]; ok {
		caller = fmt.Sprintf("%v", val)
	}

	// 拼接字段部分：request_id 优先，其他排序后输出
	fields := ""

	// request_id 优先输出
	if v, ok := entry.Data["request_id"]; ok {
		if f.ForceColor {
			fields += fmt.Sprintf("%s%v%s ",
				colorBlue, v, colorReset)
		} else {
			fields += fmt.Sprintf("%v ", v)
		}
	}

	// 其余字段排序后输出
	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if k != "caller" && k != "request_id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if f.ForceColor {
			val := fmt.Sprintf("%v", entry.Data[k])
			coloredVal := fmt.Sprintf("%s%s%s", colorWhite, val, colorReset)
			if k == "error" {
				coloredVal = fmt.Sprintf("%s%s%s", colorRed, val, colorReset)
			}
			fields += fmt.Sprintf("%s%s%s=%s ",
				colorCyan, k, colorReset, coloredVal)
		} else {
			fields += fmt.Sprintf("%s=%v ", k, entry.Data[k])
		}
	}

	fields = strings.TrimSpace(fields)

	// 拼接最终输出内容，添加颜色
	if f.ForceColor {
		coloredTimestamp := fmt.Sprintf("%s%s%s", colorGray, timestamp, resetColor)
		coloredCaller := caller
		if caller != "" {
			coloredCaller = fmt.Sprintf("%s%s%s", colorPurple, caller, resetColor)
		}
		return []byte(fmt.Sprintf("%s%-5s%s[%s] [%s] %-20s | %s\n",
			levelColor, level, resetColor, coloredTimestamp, fields, coloredCaller, entry.Message)), nil
	}

	return []byte(fmt.Sprintf("%-5s[%s] [%s] %-20s | %s\n",
		level, timestamp, fields, caller, entry.Message)), nil
}

// 初始化全局日志设置
func init() {
	ConfigureFromEnv()
}

// ConfigureFromEnv 重新从环境变量应用日志配置。
// 这允许在 main() 中加载 .env 后，让 LOG_LEVEL / LOG_PATH 立即生效。
func ConfigureFromEnv() {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	if activeLogFile != nil {
		_ = activeLogFile.Close()
		activeLogFile = nil
	}

	// 根据环境变量设置全局日志级别
	logLevel := getLogLevelFromEnv()
	appLogger.SetLevel(logLevel)

	writer := io.Writer(os.Stdout)
	logPath := resolveLogPathFromEnv()
	if logPath != "" {
		file, err := openLogFile(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logger: failed to open log file %s: %v\n", logPath, err)
		} else {
			activeLogFile = file
			writer = io.MultiWriter(os.Stdout, file)
		}
	}

	// 默认继续输出到 stdout，同时在可用时落盘到文件
	appLogger.SetOutput(writer)

	// 非终端（如 Docker 日志采集）禁用 ANSI 颜色，避免日志聚合/检索异常
	forceColor := false
	if fi, err := os.Stdout.Stat(); err == nil {
		forceColor = (fi.Mode() & os.ModeCharDevice) != 0
	}

	// 设置日志格式而不修改全局时区
	appLogger.SetFormatter(&CustomFormatter{ForceColor: forceColor})
	appLogger.SetReportCaller(false)
}

// GetLogger 获取日志实例
func GetLogger(c context.Context) *logrus.Entry {
	if logger := c.Value(types.LoggerContextKey); logger != nil {
		return logger.(*logrus.Entry)
	}
	return logrus.NewEntry(appLogger)
}

// SetOutput overrides the internal logger's output destination.
// Intended for use in tests that need to capture and assert on log content
// (e.g. verifying secrets are not written out). Restore the original writer
// (usually os.Stdout) in a defer after the test.
func SetOutput(w io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	appLogger.SetOutput(w)
}

// SetLogLevel 设置日志级别
func SetLogLevel(level LogLevel) {
	var logLevel logrus.Level

	switch level {
	case LevelDebug:
		logLevel = logrus.DebugLevel
	case LevelInfo:
		logLevel = logrus.InfoLevel
	case LevelWarn:
		logLevel = logrus.WarnLevel
	case LevelError:
		logLevel = logrus.ErrorLevel
	case LevelFatal:
		logLevel = logrus.FatalLevel
	default:
		logLevel = logrus.InfoLevel
	}

	appLogger.SetLevel(logLevel)
}

// getLogLevelFromEnv 从环境变量读取日志级别配置
func getLogLevelFromEnv() logrus.Level {
	// 从环境变量读取LOG_LEVEL配置
	logLevelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))

	switch logLevelStr {
	case "debug":
		return logrus.DebugLevel
	case "info":
		return logrus.InfoLevel
	case "warn", "warning":
		return logrus.WarnLevel
	case "error":
		return logrus.ErrorLevel
	case "fatal":
		return logrus.FatalLevel
	default:
		return logrus.DebugLevel // 无效配置时使用默认值
	}
}

func resolveLogPathFromEnv() string {
	if logPath := strings.TrimSpace(os.Getenv("LOG_PATH")); logPath != "" {
		return filepath.Clean(logPath)
	}
	return defaultMacAppLogPath()
}

func defaultMacAppLogPath() string {
	execPath, err := os.Executable()
	if err != nil || !strings.Contains(execPath, ".app/Contents/MacOS") {
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	appName := "WeKnora Lite"
	if idx := strings.Index(execPath, ".app/Contents/MacOS"); idx >= 0 {
		bundleName := filepath.Base(execPath[:idx+4])
		if trimmed := strings.TrimSuffix(bundleName, ".app"); trimmed != "" {
			appName = trimmed
		}
	}

	return filepath.Join(homeDir, "Library", "Logs", appName, appName+".log")
}

func openLogFile(logPath string) (io.WriteCloser, error) {
	dir := filepath.Dir(logPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    50, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}, nil
}

// 添加调用者字段
func addCaller(entry *logrus.Entry, skip int) *logrus.Entry {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return entry
	}
	shortFile := path.Base(file)
	funcName := "unknown"
	if fn := runtime.FuncForPC(pc); fn != nil {
		// 只保留函数名，不带包路径（如 doSomething）
		fullName := path.Base(fn.Name())
		parts := strings.Split(fullName, ".")
		funcName = parts[len(parts)-1]
	}
	return entry.WithField("caller", fmt.Sprintf("%s:%d[%s]", shortFile, line, funcName))
}

// WithRequestID 在日志中添加请求ID
func WithRequestID(c context.Context, requestID string) context.Context {
	return WithField(c, "request_id", requestID)
}

// WithField 向日志中添加一个字段
func WithField(c context.Context, key string, value interface{}) context.Context {
	logger := GetLogger(c).WithField(key, value)
	return context.WithValue(c, types.LoggerContextKey, logger)
}

// WithFields 向日志中添加多个字段
func WithFields(c context.Context, fields logrus.Fields) context.Context {
	logger := GetLogger(c).WithFields(fields)
	return context.WithValue(c, types.LoggerContextKey, logger)
}

// Debug 输出调试级别的日志
func Debug(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Debug(args...)
}

// Debugf 使用格式化字符串输出调试级别的日志
func Debugf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Debugf(format, args...)
}

// Info 输出信息级别的日志
func Info(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Info(args...)
}

// Infof 使用格式化字符串输出信息级别的日志
func Infof(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Infof(format, args...)
}

// Warn 输出警告级别的日志
func Warn(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Warn(args...)
}

// Warnf 使用格式化字符串输出警告级别的日志
func Warnf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Warnf(format, args...)
}

// Error 输出错误级别的日志
func Error(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Error(args...)
}

// Errorf 使用格式化字符串输出错误级别的日志
func Errorf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Errorf(format, args...)
}

// ErrorWithFields 输出带有额外字段的错误级别日志
func ErrorWithFields(c context.Context, err error, fields logrus.Fields) {
	if fields == nil {
		fields = logrus.Fields{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	addCaller(GetLogger(c), 2).WithFields(fields).Error("发生错误")
}

// Fatal 输出致命级别的日志并退出程序
func Fatal(c context.Context, args ...interface{}) {
	addCaller(GetLogger(c), 2).Fatal(args...)
}

// Fatalf 使用格式化字符串输出致命级别的日志并退出程序
func Fatalf(c context.Context, format string, args ...interface{}) {
	addCaller(GetLogger(c), 2).Fatalf(format, args...)
}

// CloneContext 复制上下文中的关键信息到新上下文
func CloneContext(ctx context.Context) context.Context {
	newCtx := context.Background()

	for _, k := range []types.ContextKey{
		types.LoggerContextKey,
		types.TenantIDContextKey,
		types.RequestIDContextKey,
		types.TenantInfoContextKey,
		types.UserIDContextKey,
		types.UserContextKey,
		types.LanguageContextKey,
		types.SessionTenantIDContextKey,
		types.EmbedQueryContextKey,
		// Keep the Langfuse trace alive across CloneContext boundaries so
		// LLM/Embedder/Reranker/VLM/ASR wrappers attach their generations
		// to the same trace opened by GinMiddleware, instead of each call
		// auto-creating its own orphan trace.
		types.LangfuseTraceContextKey,
	} {
		if v := ctx.Value(k); v != nil {
			newCtx = context.WithValue(newCtx, k, v)
		}
	}

	return newCtx
}
