package router

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultOperationsAlertCheckInterval = time.Minute
	defaultOperationsAlertCooldown      = 5 * time.Minute
	defaultOperationsAlertTimeout       = 5 * time.Second
	operationsAlertCheckTimeout         = 10 * time.Second

	smtpTLSStartTLS = "starttls"
	smtpTLSImplicit = "implicit"
	smtpTLSNone     = "none"
)

// operationsEmailAlertConfig is intentionally environment-only. SMTP
// credentials must not be stored in the database or exposed by status APIs.
type operationsEmailAlertConfig struct {
	Enabled       bool
	To            []string
	From          string
	Host          string
	Port          int
	Username      string
	Password      string
	TLSMode       string
	CheckInterval time.Duration
	Cooldown      time.Duration
	Timeout       time.Duration
}

type operationsAlertCondition struct {
	Key      string
	Severity string
	Summary  string
}

type operationsAlertState struct {
	Active           bool
	FailureDelivered bool
	RecoveryPending  bool
	LastAttempt      time.Time
	LastFailureLog   time.Time
}

type operationsEmailSender interface {
	Send(context.Context, string, string) error
}

type smtpOperationsEmailSender struct {
	config operationsEmailAlertConfig
}

type operationsEmailAlerter struct {
	config          operationsEmailAlertConfig
	sender          operationsEmailSender
	now             func() time.Time
	onDeliveryError func(string)

	mu     sync.Mutex
	states map[string]operationsAlertState
}

func newOperationsEmailAlerterFromEnv() *operationsEmailAlerter {
	config, warnings := loadOperationsEmailAlertConfigFromEnv()
	for _, warning := range warnings {
		logger.Warnf(context.Background(), "[OperationsAlert] %s", warning)
	}
	if !config.Enabled {
		return nil
	}

	return newOperationsEmailAlerter(config, smtpOperationsEmailSender{config: config})
}

func newOperationsEmailAlerter(config operationsEmailAlertConfig, sender operationsEmailSender) *operationsEmailAlerter {
	return &operationsEmailAlerter{
		config: config,
		sender: sender,
		now:    time.Now,
		onDeliveryError: func(key string) {
			logger.Warnf(context.Background(), "[OperationsAlert] email delivery failed for alert %s; details suppressed", key)
		},
		states: make(map[string]operationsAlertState),
	}
}

func loadOperationsEmailAlertConfigFromEnv() (operationsEmailAlertConfig, []string) {
	enabled, warning := parseOperationsAlertBoolEnv("OPS_ALERT_EMAIL_ENABLED", false)
	if warning != "" {
		return operationsEmailAlertConfig{}, []string{warning}
	}
	if !enabled {
		return operationsEmailAlertConfig{}, nil
	}

	config := operationsEmailAlertConfig{
		Enabled:       true,
		Host:          strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port:          587,
		Username:      strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		TLSMode:       strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS_MODE"))),
		CheckInterval: defaultOperationsAlertCheckInterval,
		Cooldown:      defaultOperationsAlertCooldown,
		Timeout:       defaultOperationsAlertTimeout,
	}
	if config.TLSMode == "" {
		config.TLSMode = smtpTLSStartTLS
	}

	warnings := make([]string, 0)
	var toWarning string
	config.To, toWarning = parseOperationsAlertRecipients(os.Getenv("OPS_ALERT_EMAIL_TO"))
	if toWarning != "" {
		warnings = append(warnings, toWarning)
	}
	var fromWarning string
	config.From, fromWarning = parseOperationsAlertAddress(os.Getenv("SMTP_FROM"), "SMTP_FROM")
	if fromWarning != "" {
		warnings = append(warnings, fromWarning)
	}
	if config.Host == "" || strings.ContainsAny(config.Host, "\r\n") {
		warnings = append(warnings, "OPS_ALERT_EMAIL_ENABLED requires a valid SMTP_HOST")
	}

	if rawPort := strings.TrimSpace(os.Getenv("SMTP_PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			warnings = append(warnings, "SMTP_PORT must be an integer from 1 to 65535")
		} else {
			config.Port = port
		}
	}
	if config.TLSMode != smtpTLSStartTLS && config.TLSMode != smtpTLSImplicit && config.TLSMode != smtpTLSNone {
		warnings = append(warnings, "SMTP_TLS_MODE must be starttls, implicit, or none")
	}

	password, passwordWarning := loadOperationsSMTPPassword()
	if passwordWarning != "" {
		warnings = append(warnings, passwordWarning)
	} else {
		config.Password = password
	}
	if (config.Username == "") != (config.Password == "") {
		warnings = append(warnings, "SMTP_USERNAME and an SMTP password must be configured together")
	}
	if config.TLSMode == smtpTLSNone && config.Username != "" {
		warnings = append(warnings, "SMTP_TLS_MODE=none cannot be used with SMTP authentication")
	}

	config.CheckInterval, warning = parseOperationsAlertSecondsEnv(
		"OPS_ALERT_CHECK_INTERVAL_SECONDS", defaultOperationsAlertCheckInterval, 10, 86400,
	)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	config.Cooldown, warning = parseOperationsAlertSecondsEnv(
		"OPS_ALERT_COOLDOWN_SECONDS", defaultOperationsAlertCooldown, 60, 604800,
	)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	config.Timeout, warning = parseOperationsAlertSecondsEnv(
		"OPS_ALERT_TIMEOUT_SECONDS", defaultOperationsAlertTimeout, 1, 300,
	)
	if warning != "" {
		warnings = append(warnings, warning)
	}

	if len(warnings) > 0 {
		return operationsEmailAlertConfig{}, warnings
	}
	return config, nil
}

func parseOperationsAlertBoolEnv(name string, defaultValue bool) (bool, string) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, ""
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultValue, fmt.Sprintf("%s must be a boolean; email alerting remains disabled", name)
	}
	return value, ""
}

func parseOperationsAlertRecipients(raw string) ([]string, string) {
	parts := strings.Split(raw, ",")
	addresses := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		address, warning := parseOperationsAlertAddress(part, "OPS_ALERT_EMAIL_TO")
		if warning != "" {
			return nil, "OPS_ALERT_EMAIL_TO must contain one or more valid email addresses"
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return nil, "OPS_ALERT_EMAIL_TO must contain one or more valid email addresses"
	}
	return addresses, ""
}

func parseOperationsAlertAddress(raw, name string) (string, string) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || parsed.Address == "" {
		return "", fmt.Sprintf("%s must be a valid email address", name)
	}
	return parsed.Address, ""
}

func loadOperationsSMTPPassword() (string, string) {
	value := os.Getenv("SMTP_PASSWORD")
	path := strings.TrimSpace(os.Getenv("SMTP_PASSWORD_FILE"))
	if value != "" && path != "" {
		return "", "configure only one of SMTP_PASSWORD or SMTP_PASSWORD_FILE"
	}
	if path == "" {
		return value, ""
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", "SMTP_PASSWORD_FILE could not be read"
	}
	return strings.TrimSpace(string(contents)), ""
}

func parseOperationsAlertSecondsEnv(name string, defaultValue time.Duration, minimum, maximum int) (time.Duration, string) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, ""
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < minimum || seconds > maximum {
		return defaultValue, fmt.Sprintf("%s must be an integer from %d to %d", name, minimum, maximum)
	}
	return time.Duration(seconds) * time.Second, ""
}

func (a *operationsEmailAlerter) start(check func(context.Context) []operationsAlertCondition) func() {
	stop := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		checkAndProcess := func() {
			ctx, cancel := context.WithTimeout(context.Background(), operationsAlertCheckTimeout)
			defer cancel()
			a.process(a.now(), check(ctx))
		}

		checkAndProcess()
		ticker := time.NewTicker(a.config.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				checkAndProcess()
			case <-stop:
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() { close(stop) })
	}
}

func (a *operationsEmailAlerter) process(now time.Time, conditions []operationsAlertCondition) {
	active := make(map[string]operationsAlertCondition, len(conditions))
	for _, condition := range conditions {
		active[condition.Key] = condition
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for key, state := range a.states {
		if _, stillActive := active[key]; stillActive || !state.Active {
			continue
		}
		state.Active = false
		if !state.FailureDelivered {
			delete(a.states, key)
			continue
		}
		state.RecoveryPending = true
		if a.deliver(now, key, &state, "recovery", "recovered") {
			delete(a.states, key)
			continue
		}
		a.states[key] = state
	}

	for key, condition := range active {
		state, known := a.states[key]
		if !known {
			state.Active = true
			a.deliver(now, key, &state, condition.Severity, condition.Summary)
			a.states[key] = state
			continue
		}
		if !state.Active {
			state.Active = true
			state.RecoveryPending = false
			if !state.FailureDelivered {
				a.deliver(now, key, &state, condition.Severity, condition.Summary)
			}
			a.states[key] = state
			continue
		}
		if !state.FailureDelivered && now.Sub(state.LastAttempt) >= a.config.Cooldown {
			a.deliver(now, key, &state, condition.Severity, condition.Summary)
			a.states[key] = state
		}
	}

	for key, state := range a.states {
		if !state.Active && state.RecoveryPending && now.Sub(state.LastAttempt) >= a.config.Cooldown {
			if a.deliver(now, key, &state, "recovery", "recovered") {
				delete(a.states, key)
			} else {
				a.states[key] = state
			}
		}
	}
}

func (a *operationsEmailAlerter) deliver(now time.Time, key string, state *operationsAlertState, severity, summary string) bool {
	state.LastAttempt = now
	ctx, cancel := context.WithTimeout(context.Background(), a.config.Timeout)
	err := a.sender.Send(ctx, operationsAlertSubject(severity, summary), operationsAlertBody(severity, summary, now))
	cancel()
	if err == nil {
		if severity == "recovery" {
			state.RecoveryPending = false
		} else {
			state.FailureDelivered = true
		}
		return true
	}
	if state.LastFailureLog.IsZero() || now.Sub(state.LastFailureLog) >= a.config.Cooldown {
		state.LastFailureLog = now
		a.onDeliveryError(key)
	}
	return false
}

func operationsAlertSubject(severity, summary string) string {
	return fmt.Sprintf("WeKnora %s alert: %s", severity, summary)
}

func operationsAlertBody(severity, summary string, now time.Time) string {
	return fmt.Sprintf(
		"WeKnora operations status changed.\r\n\r\nSeverity: %s\r\nCondition: %s\r\nTime: %s\r\n\r\nInspect the protected operations status endpoint and the deployment logs. This email intentionally excludes credentials and raw dependency errors.\r\n",
		severity,
		summary,
		now.UTC().Format(time.RFC3339),
	)
}

func operationAlertConditions(status operationsStatusResponse) []operationsAlertCondition {
	conditions := make([]operationsAlertCondition, 0, 4)
	if status.Dependencies["database"] == "failed" {
		conditions = append(conditions, operationsAlertCondition{
			Key: "database_unavailable", Severity: "critical", Summary: "database dependency unavailable",
		})
	}
	if status.Dependencies["redis"] == "failed" {
		conditions = append(conditions, operationsAlertCondition{
			Key: "redis_unavailable", Severity: "critical", Summary: "Redis dependency unavailable",
		})
	}
	if status.Migration.Known && status.Migration.Dirty {
		conditions = append(conditions, operationsAlertCondition{
			Key: "schema_migration_dirty", Severity: "critical", Summary: "schema migration is marked dirty",
		})
	}
	if status.FileLog.Enabled && (status.FileLog.DiskState == "warning" || status.FileLog.DiskState == "critical") {
		conditions = append(conditions, operationsAlertCondition{
			Key:      "log_disk_space_low",
			Severity: status.FileLog.DiskState,
			Summary:  "application log filesystem space is low",
		})
	}
	return conditions
}

func (s smtpOperationsEmailSender) Send(ctx context.Context, subject, body string) error {
	address := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}

	if s.config.TLSMode == smtpTLSImplicit {
		tlsConnection := tls.Client(connection, &tls.Config{
			ServerName: s.config.Host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return err
		}
		connection = tlsConnection
	}

	client, err := smtp.NewClient(connection, s.config.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.config.TLSMode == smtpTLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.config.From); err != nil {
		return err
	}
	for _, recipient := range s.config.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := "From: " + s.config.From + "\r\n" +
		"To: " + strings.Join(s.config.To, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" + body
	if _, err := io.WriteString(writer, message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
